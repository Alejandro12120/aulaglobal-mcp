package selfupdate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The installers (install.sh, install.ps1) embed their own copy of the signing
// key and know the archive naming. These tests make them fail loudly, on every
// OS of the CI matrix, if they ever drift from the Go code and update_key.pub.

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func installerKey(t *testing.T, file string, re *regexp.Regexp) string {
	t.Helper()
	m := re.FindStringSubmatch(readRepoFile(t, file))
	if m == nil {
		t.Fatalf("%s: signing key line not found", file)
	}
	return strings.TrimSpace(m[1])
}

func TestInstallersEmbedTheTrustedKey(t *testing.T) {
	want := strings.TrimSpace(readRepoFile(t, "internal/selfupdate/update_key.pub"))
	if want != TrustedKey() {
		t.Fatalf("update_key.pub on disk differs from the embedded key")
	}
	cases := []struct {
		file string
		re   *regexp.Regexp
	}{
		{"install.sh", regexp.MustCompile(`(?m)^SIGN_KEY='([^']+)'\s*$`)},
		{"install.ps1", regexp.MustCompile(`(?m)^\s*\$SignKey = '([^']+)'\s*$`)},
	}
	for _, c := range cases {
		if got := installerKey(t, c.file, c.re); got != want {
			t.Errorf("%s embeds a different signing key than internal/selfupdate/update_key.pub:\n got  %q\n want %q", c.file, got, want)
		}
	}
}

func TestInstallersMatchReleaseNaming(t *testing.T) {
	platforms := []struct{ goos, goarch string }{
		{"darwin", "arm64"}, {"darwin", "amd64"}, {"linux", "amd64"},
		{"linux", "arm64"}, {"windows", "amd64"}, {"windows", "arm64"},
	}
	sh := readRepoFile(t, "install.sh")
	ps := readRepoFile(t, "install.ps1")
	for _, p := range platforms {
		name, err := AssetName("1.2.3", p.goos, p.goarch)
		if err != nil {
			t.Fatal(err)
		}
		label := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(name, BinaryName+"_1.2.3_"), ".tar.gz"), ".zip")
		script, text := sh, "install.sh"
		if p.goos == "windows" {
			script, text = ps, "install.ps1"
		}
		if !strings.Contains(script, label) {
			t.Errorf("%s does not mention platform label %q used by AssetName(%s/%s)", text, label, p.goos, p.goarch)
		}
	}
	for _, f := range []struct{ name, text string }{{"install.sh", sh}, {"install.ps1", ps}} {
		for _, want := range []string{Repo, Namespace, BinaryName} {
			if !strings.Contains(f.text, want) {
				t.Errorf("%s does not contain %q", f.name, want)
			}
		}
	}
	if !strings.Contains(sh, `${BIN_NAME}_${VERSION}_${PLATFORM}.tar.gz`) {
		t.Errorf("install.sh archive name template changed; update this test and .goreleaser.yaml together")
	}
	if !strings.Contains(ps, `'{0}_{1}_{2}.zip' -f $BinName, $version, $platform`) {
		t.Errorf("install.ps1 archive name template changed; update this test and .goreleaser.yaml together")
	}
}

func TestInstallerFileHygiene(t *testing.T) {
	ps := readRepoFile(t, "install.ps1")
	// Windows PowerShell 5.1 may mis-decode non-ASCII text served by "irm | iex".
	for i, r := range ps {
		if r > 0x7e || (r < 0x20 && r != '\n' && r != '\r' && r != '\t') {
			t.Fatalf("install.ps1 must be plain ASCII: rune %U at byte %d", r, i)
		}
	}
	sh := readRepoFile(t, "install.sh")
	if strings.Contains(sh, "\r") {
		t.Errorf("install.sh must use LF line endings")
	}
	if !strings.HasPrefix(sh, "#!/bin/sh\n") {
		t.Errorf("install.sh must start with a #!/bin/sh shebang")
	}
	if !strings.HasSuffix(strings.TrimSpace(sh), `main "$@"`) {
		t.Errorf("install.sh must end by calling main, so a truncated download runs nothing")
	}
}
