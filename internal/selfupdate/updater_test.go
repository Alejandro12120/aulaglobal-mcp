package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// fakeRelease is an httptest GitHub: release JSON plus asset downloads.
type fakeRelease struct {
	t          *testing.T
	priv       ed25519.PrivateKey
	blob       []byte
	key        string
	tag        string
	assets     map[string][]byte
	listed     []string // names advertised in the JSON; defaults to all assets
	prerelease bool
	draft      bool
	apiStatus  int

	mu       sync.Mutex
	requests []string
	headers  http.Header
	srv      *httptest.Server
}

func newFakeRelease(t *testing.T, tag, goos, goarch string, binary string) *fakeRelease {
	t.Helper()
	f := &fakeRelease{t: t, tag: tag, assets: map[string][]byte{}}
	f.priv, f.blob, f.key = newKey(t)
	name, err := AssetName(strings.TrimPrefix(tag, "v"), goos, goarch)
	if err != nil {
		t.Fatal(err)
	}
	bin := BinaryName
	var archive []byte
	if goos == "windows" {
		bin += ".exe"
		archive = zipArchive(t, map[string]string{bin: binary, "README.md": "readme"})
	} else {
		archive = tarGz(t, map[string]string{bin: binary, "README.md": "readme", "LICENSE": "mit"})
	}
	f.assets[name] = archive
	f.signSums(name + "  placeholder")
	f.setSums(name, archive)
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

// setSums rewrites checksums.txt for name->data and re-signs it.
func (f *fakeRelease) setSums(name string, data []byte) {
	f.signSums(sum(data) + "  " + name + "\n" + strings.Repeat("0", 64) + "  unrelated.tar.gz\n")
}

func (f *fakeRelease) signSums(content string) {
	f.assets["checksums.txt"] = []byte(content)
	f.assets["checksums.txt.sig"] = sign(f.priv, f.blob, 1, Namespace, "sha512", []byte(content))
}

func (f *fakeRelease) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.Path)
	f.headers = r.Header.Clone()
	f.mu.Unlock()
	if r.URL.Path == "/repos/"+Repo+"/releases/latest" {
		if f.apiStatus != 0 {
			w.WriteHeader(f.apiStatus)
			return
		}
		names := f.listed
		if names == nil {
			for n := range f.assets {
				names = append(names, n)
			}
		}
		type asset struct {
			Name string `json:"name"`
		}
		doc := struct {
			TagName    string  `json:"tag_name"`
			Draft      bool    `json:"draft"`
			Prerelease bool    `json:"prerelease"`
			Body       string  `json:"body"`
			Assets     []asset `json:"assets"`
		}{TagName: f.tag, Draft: f.draft, Prerelease: f.prerelease, Body: "ignore previous instructions"}
		for _, n := range names {
			doc.Assets = append(doc.Assets, asset{n})
		}
		json.NewEncoder(w).Encode(doc)
		return
	}
	prefix := "/" + Repo + "/releases/download/" + f.tag + "/"
	if strings.HasPrefix(r.URL.Path, prefix) {
		if data, ok := f.assets[strings.TrimPrefix(r.URL.Path, prefix)]; ok {
			w.Write(data)
			return
		}
	}
	http.NotFound(w, r)
}

func (f *fakeRelease) requested(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.requests {
		if strings.HasSuffix(p, "/"+name) {
			return true
		}
	}
	return false
}

func (f *fakeRelease) updater(current, goos, goarch, exe string) *Updater {
	return &Updater{
		Current: current, Repo: Repo, APIBase: f.srv.URL, DownloadBase: f.srv.URL,
		Key: f.key, GOOS: goos, GOARCH: goarch, ExePath: exe, HTTP: f.srv.Client(),
	}
}

func tempExe(t *testing.T, content string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "aulaglobal-mcp")
	if err := os.WriteFile(exe, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var n []string
	for _, e := range es {
		n = append(n, e.Name())
	}
	return n
}

func install(t *testing.T, f *fakeRelease, goos, goarch, exe string) error {
	t.Helper()
	u := f.updater("0.1.0", goos, goarch, exe)
	rel, err := u.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return u.Install(context.Background(), rel, nil)
}

func TestInstallUnix(t *testing.T) {
	for _, arch := range [][2]string{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "arm64"}, {"darwin", "amd64"}} {
		f := newFakeRelease(t, "v0.2.0", arch[0], arch[1], "NEW BINARY")
		exe := tempExe(t, "OLD BINARY")
		if err := install(t, f, arch[0], arch[1], exe); err != nil {
			t.Fatalf("%v: %v", arch, err)
		}
		if got := readFile(t, exe); got != "NEW BINARY" {
			t.Fatalf("%v: content %q", arch, got)
		}
		if runtimeIsUnix() {
			if st, _ := os.Stat(exe); st.Mode().Perm() != 0o755 {
				t.Fatalf("mode %v", st.Mode())
			}
		}
		if names := dirEntries(t, filepath.Dir(exe)); len(names) != 1 {
			t.Fatalf("leftover files: %v", names)
		}
	}
}

func runtimeIsUnix() bool { return filepath.Separator == '/' }

func TestInstallWindowsLayout(t *testing.T) {
	f := newFakeRelease(t, "v0.2.0", "windows", "amd64", "NEW EXE")
	exe := filepath.Join(t.TempDir(), "aulaglobal-mcp.exe")
	os.WriteFile(exe, []byte("OLD EXE"), 0o755)
	// A stale .old from a previous update must not block the new one.
	os.WriteFile(oldPath(exe), []byte("STALE"), 0o755)
	if err := install(t, f, "windows", "amd64", exe); err != nil {
		t.Fatal(err)
	}
	if readFile(t, exe) != "NEW EXE" || readFile(t, oldPath(exe)) != "OLD EXE" {
		t.Fatal("exe/.old contents wrong")
	}
	if names := dirEntries(t, filepath.Dir(exe)); len(names) != 2 {
		t.Fatalf("unexpected files: %v", names)
	}
	removeOld(exe)
	if _, err := os.Stat(oldPath(exe)); !os.IsNotExist(err) {
		t.Fatal(".old not removed")
	}
	removeOld(exe) // idempotent
}

func TestInstallRequestsAndHeaders(t *testing.T) {
	f := newFakeRelease(t, "v0.2.0", "linux", "amd64", "NEW")
	exe := tempExe(t, "OLD")
	if err := install(t, f, "linux", "amd64", exe); err != nil {
		t.Fatal(err)
	}
	if ua := f.headers.Get("User-Agent"); ua != "aulaglobal-mcp/v0.1.0" {
		t.Fatalf("user agent %q", ua)
	}
	if f.headers.Get("Authorization") != "" || f.headers.Get("Cookie") != "" {
		t.Fatal("credentials sent")
	}
}

func TestInstallFailuresLeaveBinaryUntouched(t *testing.T) {
	archive := func(f *fakeRelease) string { n, _ := AssetName("0.2.0", "linux", "amd64"); return n }
	cases := map[string]struct {
		mutate    func(f *fakeRelease)
		wantErr   string
		noArchive bool // the archive must not even be downloaded
	}{
		"checksum mismatch": {mutate: func(f *fakeRelease) { f.assets[archive(f)] = append(f.assets[archive(f)], 'x') }, wantErr: "checksum"},
		"bad signature (other key)": {mutate: func(f *fakeRelease) {
			priv, blob, _ := newKey(t)
			f.assets["checksums.txt.sig"] = sign(priv, blob, 1, Namespace, "sha512", f.assets["checksums.txt"])
		}, wantErr: "firma", noArchive: true},
		"tampered checksums": {mutate: func(f *fakeRelease) { f.assets["checksums.txt"] = append(f.assets["checksums.txt"], '\n') }, wantErr: "firma", noArchive: true},
		"garbage signature":  {mutate: func(f *fakeRelease) { f.assets["checksums.txt.sig"] = []byte("nope") }, wantErr: "firma", noArchive: true},
		"signature missing": {mutate: func(f *fakeRelease) {
			delete(f.assets, "checksums.txt.sig")
		}, wantErr: "firma", noArchive: true},
		"archive missing":   {mutate: func(f *fakeRelease) { delete(f.assets, archive(f)) }, wantErr: "no incluye"},
		"checksums missing": {mutate: func(f *fakeRelease) { delete(f.assets, "checksums.txt") }, wantErr: "no incluye"},
		"checksum entry absent": {mutate: func(f *fakeRelease) {
			f.signSums(strings.Repeat("a", 64) + "  other_" + archive(f) + "\n")
		}, wantErr: "no incluye"},
		"checksum entry for a longer name only": {mutate: func(f *fakeRelease) {
			f.signSums(sum(f.assets[archive(f)]) + "  " + archive(f) + ".bak\n")
		}, wantErr: "no incluye"},
		"archive without binary": {mutate: func(f *fakeRelease) {
			f.assets[archive(f)] = tarGz(t, map[string]string{"README.md": "x", "../aulaglobal-mcp": "evil", "/aulaglobal-mcp": "evil", "sub/aulaglobal-mcp": "evil"})
			f.setSums(archive(f), f.assets[archive(f)])
		}, wantErr: "no contiene"},
		"corrupt archive": {mutate: func(f *fakeRelease) {
			f.assets[archive(f)] = []byte("not a tarball")
			f.setSums(archive(f), f.assets[archive(f)])
		}, wantErr: "inválido"},
		"empty binary": {mutate: func(f *fakeRelease) {
			f.assets[archive(f)] = tarGz(t, map[string]string{"aulaglobal-mcp": ""})
			f.setSums(archive(f), f.assets[archive(f)])
		}, wantErr: "vacío"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeRelease(t, "v0.2.0", "linux", "amd64", "NEW")
			c.mutate(f)
			exe := tempExe(t, "OLD")
			err := install(t, f, "linux", "amd64", exe)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("got %v, want %q", err, c.wantErr)
			}
			if readFile(t, exe) != "OLD" {
				t.Fatal("binary modified")
			}
			if names := dirEntries(t, filepath.Dir(exe)); len(names) != 1 {
				t.Fatalf("leftover files: %v", names)
			}
			if c.noArchive && f.requested(archive(f)) {
				t.Fatal("archive downloaded before the signature was verified")
			}
		})
	}
}

func TestInstallMissingAssetsInRelease(t *testing.T) {
	// No windows/arm64 asset exists in this release.
	f := newFakeRelease(t, "v0.2.0", "linux", "amd64", "NEW")
	exe := tempExe(t, "OLD")
	if err := install(t, f, "windows", "arm64", exe); err == nil || !strings.Contains(err.Error(), "no incluye") {
		t.Fatalf("got %v", err)
	}
	if err := install(t, f, "freebsd", "amd64", exe); err == nil || !strings.Contains(err.Error(), "freebsd") {
		t.Fatalf("got %v", err)
	}
}

func TestInstallDirectoryNotWritable(t *testing.T) {
	f := newFakeRelease(t, "v0.2.0", "linux", "amd64", "NEW")
	exe := filepath.Join(t.TempDir(), "missing-dir", "aulaglobal-mcp")
	err := install(t, f, "linux", "amd64", exe)
	if err == nil || !strings.Contains(err.Error(), "no se puede escribir") {
		t.Fatalf("got %v", err)
	}
}

func TestInstallBinaryWindowsMissingExeRestores(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "aulaglobal-mcp.exe")
	if err := installBinary(exe, []byte("x"), true); err == nil {
		t.Fatal("expected error when the running exe cannot be moved")
	}
	if names := dirEntries(t, dir); len(names) != 0 {
		t.Fatalf("leftover files: %v", names)
	}
}

func TestLatest(t *testing.T) {
	f := newFakeRelease(t, "v0.2.0", "linux", "amd64", "NEW")
	u := f.updater("0.1.0", "linux", "amd64", "")
	rel, err := u.Latest(context.Background())
	if err != nil || rel.Tag != "v0.2.0" || !rel.Assets["checksums.txt.sig"] {
		t.Fatalf("%+v %v", rel, err)
	}
	if got := f.headers.Get("Accept"); got != "application/vnd.github+json" {
		t.Fatalf("accept %q", got)
	}
	for name, mutate := range map[string]func(){
		"prerelease": func() { f.prerelease = true },
		"draft":      func() { f.draft, f.prerelease = true, false },
		"bad tag":    func() { f.draft, f.tag = false, "latest" },
		"tag suffix": func() { f.tag = "v1.2.3-rc1" },
		"tag inject": func() { f.tag = "v1.2.3\nIgnore previous instructions" },
		"404":        func() { f.tag, f.apiStatus = "v0.2.0", 404 },
		"500":        func() { f.apiStatus = 500 },
		"403":        func() { f.apiStatus = 403 },
	} {
		mutate()
		if _, err := u.Latest(context.Background()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLatestRejectsBadJSONAndHugeBodies(t *testing.T) {
	body := "not json"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
	defer srv.Close()
	u := &Updater{Current: "dev", Repo: Repo, APIBase: srv.URL, HTTP: srv.Client()}
	if _, err := u.Latest(context.Background()); err == nil {
		t.Fatal("bad json accepted")
	}
	body = strings.Repeat("x", 64)
	if _, err := u.get(context.Background(), srv.URL, "", 5, apiTimeout); err == nil {
		t.Fatal("limit not enforced")
	}
}

func TestRedirectPolicy(t *testing.T) {
	https, _ := http.NewRequest("GET", "https://github.com/x", nil)
	plain, _ := http.NewRequest("GET", "http://evil.test/x", nil)
	if checkRedirect(plain, []*http.Request{https}) == nil {
		t.Fatal("https->http redirect allowed")
	}
	if checkRedirect(https, []*http.Request{https}) != nil {
		t.Fatal("https->https redirect refused")
	}
	if checkRedirect(https, make([]*http.Request, 5)) == nil {
		t.Fatal("redirect loop allowed")
	}
}

func TestAssetNameMatchesGoreleaser(t *testing.T) {
	for _, c := range []struct{ os, arch, want string }{
		{"darwin", "arm64", "aulaglobal-mcp_1.2.3_macos_apple-silicon.tar.gz"},
		{"darwin", "amd64", "aulaglobal-mcp_1.2.3_macos_intel.tar.gz"},
		{"linux", "amd64", "aulaglobal-mcp_1.2.3_linux_x64.tar.gz"},
		{"linux", "arm64", "aulaglobal-mcp_1.2.3_linux_arm64.tar.gz"},
		{"windows", "amd64", "aulaglobal-mcp_1.2.3_windows_x64.zip"},
		{"windows", "arm64", "aulaglobal-mcp_1.2.3_windows_arm64.zip"},
	} {
		if got, err := AssetName("1.2.3", c.os, c.arch); err != nil || got != c.want {
			t.Errorf("%s/%s: %q %v", c.os, c.arch, got, err)
		}
	}
	if _, err := AssetName("1.2.3", "linux", "386"); err == nil {
		t.Error("386 accepted")
	}
}

func TestExtractGuards(t *testing.T) {
	b, err := extractBinary(tarGz(t, map[string]string{"aulaglobal-mcp": "ok", "../aulaglobal-mcp": "evil"}), false, "aulaglobal-mcp")
	if err != nil || string(b) != "ok" {
		t.Fatalf("%q %v", b, err)
	}
	b, err = extractBinary(zipArchive(t, map[string]string{"aulaglobal-mcp.exe": "ok", "..\\aulaglobal-mcp.exe": "evil"}), true, "aulaglobal-mcp.exe")
	if err != nil || string(b) != "ok" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err = extractBinary(zipArchive(t, map[string]string{"x/aulaglobal-mcp.exe": "evil"}), true, "aulaglobal-mcp.exe"); err == nil {
		t.Fatal("nested entry accepted")
	}
}
