package main

import (
	"aulaglobal-mcp/internal/aulaglobal"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestSetupOffline(t *testing.T) {
	var site string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "launch.php") {
			c, e := r.Cookie("MoodleSessionag")
			if e != nil || c.Value != "abc123" {
				t.Error("cookie incorrect")
			}
			hash := md5.Sum([]byte(site + r.URL.Query().Get("passport")))
			w.Header().Set("Location", "moodlemobile://token="+base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(hash[:])+":::fake-token:::private")))
			w.WriteHeader(303)
			return
		}
		r.ParseForm()
		if r.Form.Get("wstoken") != "fake-token" {
			t.Error("token incorrect")
		}
		fmt.Fprint(w, `{"fullname":"Ana","userid":1}`)
	}))
	defer ts.Close()
	site = ts.URL
	root := t.TempDir()
	secrets := filepath.Join(root, "secrets")
	os.Mkdir(secrets, 0700)
	token := filepath.Join(secrets, "token")
	for _, name := range []string{"token.meta.json", "token.private", ".token-123", "token.notes"} {
		os.WriteFile(filepath.Join(secrets, name), []byte("old"), 0600)
	}
	var out strings.Builder
	attempt := 0
	u := setupUI{strings.NewReader(""), &out, func() ([]byte, error) {
		attempt++
		if attempt == 1 {
			return []byte(""), nil
		}
		return []byte(` "MoodleSessionag=abc123" `), nil
	}}
	ok, e := acquire(context.Background(), aulaglobal.New(site, nil), token, filepath.Join(root, "data"), u)
	if e != nil || !ok {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "Token válido. Hola, Ana") || strings.Contains(out.String(), "abc123") {
		t.Fatal(out.String())
	}
	for n := 1; n <= 3; n++ {
		if !strings.Contains(out.String(), fmt.Sprintf("\n── Paso %d de 4", n)) {
			t.Fatal(out.String())
		}
	}
	if strings.Contains(out.String(), "\033") {
		t.Fatal("ANSI in redirected output")
	}
	t.Log("\n" + out.String())
	if _, e = os.Stat(filepath.Join(secrets, ".token-123")); !os.IsNotExist(e) {
		t.Fatal("temporary retained")
	}
	if b, _ := os.ReadFile(filepath.Join(secrets, "token.notes")); string(b) != "old" {
		t.Fatal("unrelated file changed")
	}
	var decline strings.Builder
	u = setupUI{strings.NewReader("\n"), &decline, func() ([]byte, error) { t.Fatal("password read after decline"); return nil, nil }}
	_, e = acquire(context.Background(), aulaglobal.New(site, nil), token, root, u)
	if e != nil || !strings.Contains(decline.String(), "Obtenido el:") {
		t.Fatal(e, decline.String())
	}
}
func TestNormalize(t *testing.T) {
	for _, s := range []string{" abc123 ", `"MoodleSessionag=abc123"`, "'abc123'", "MoodleSessionag=\"abc123\""} {
		if normalizeCookie(s) != "abc123" {
			t.Fatal(s)
		}
	}
}
func TestConfigMerging(t *testing.T) {
	entry := map[string]any{"command": `C:\Users\Ana\aulaglobal-mcp.exe`, "args": []string{"--transport", "stdio"}}
	old := []byte(`{"other":{"large":1234567890123456789},"mcpServers":{"keep":{"command":"keep"}}}`)
	b, e := claudeConfig(old, entry)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(b), "1234567890123456789") || !strings.Contains(string(b), "\"keep\"") {
		t.Fatal(string(b))
	}
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, old, 0600)
	if e = atomicConfig(path, b); e != nil {
		t.Fatal(e)
	}
	bak, _ := os.ReadFile(path + ".bak")
	if string(bak) != string(old) {
		t.Fatal("backup")
	}
	b2, _ := os.ReadFile(path)
	if string(b2) != string(b) {
		t.Fatal("write")
	}
	for _, old := range []string{"# keep\nmodel = 'x'\n[mcp_servers.other]\ncommand='keep'\n", "model='x'\n[mcp_servers.aulaglobal]\ncommand='old'\n[mcp_servers.aulaglobal.env]\nX='x'\n[mcp_servers.other]\ncommand='keep'\n", "model='x'\n[mcp_servers.\"aulaglobal\"]\ncommand='old'\n"} {
		b, exists, e := codexConfig([]byte(old), entry, false)
		if e != nil {
			t.Fatal(e)
		}
		if exists {
			if b != nil {
				t.Fatal("must confirm")
			}
			b, _, e = codexConfig([]byte(old), entry, true)
		}
		if e != nil || !strings.Contains(string(b), "model") || strings.Contains(string(b), "command='old'") {
			t.Fatal(e, string(b))
		}
		if strings.Contains(old, "# keep") && !strings.Contains(string(b), "# keep") {
			t.Fatal("comment lost")
		}
	}
	if _, e = claudeConfig([]byte("{bad"), entry); e == nil {
		t.Fatal("invalid json accepted")
	}
	if _, _, e = codexConfig([]byte("[bad"), entry, true); e == nil {
		t.Fatal("invalid toml accepted")
	}
	var root map[string]any
	if e = json.Unmarshal(b, &root); e != nil {
		t.Fatal(e)
	}
}

func TestRegisterConfirmationsAndBackups(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, "Claude", "config.json")
	codex := filepath.Join(dir, ".codex", "config.toml")
	os.MkdirAll(filepath.Dir(claude), 0700)
	os.MkdirAll(filepath.Dir(codex), 0700)
	originalJSON := []byte(`{"theme":"dark","mcpServers":{"aulaglobal":{"command":"old"},"other":{"command":"keep"}}}`)
	originalTOML := []byte("# keep\nmodel='x'\n[mcp_servers.aulaglobal]\ncommand='old'\n[mcp_servers.other]\ncommand='keep'\n")
	os.WriteFile(claude, originalJSON, 0600)
	os.WriteFile(codex, originalTOML, 0600)
	var out strings.Builder
	u := setupUI{strings.NewReader("s\ns\ns\ns\n"), &out, nil}
	if e := register(u, "/absolute/program", "/absolute/token", "/absolute/data", claude, codex); e != nil {
		t.Fatal(e)
	}
	for path, want := range map[string][]byte{claude: originalJSON, codex: originalTOML} {
		bak, _ := os.ReadFile(path + ".bak")
		if string(bak) != string(want) {
			t.Fatal("backup", path)
		}
		b, _ := os.ReadFile(path)
		if !strings.Contains(string(b), "keep") || !strings.Contains(string(b), "/absolute/program") {
			t.Fatal(string(b))
		}
	}
	// Declining replacement leaves both the config and its backup untouched.
	before, _ := os.ReadFile(codex)
	u.in = strings.NewReader("n\ns\nn\n")
	if e := register(u, "changed", "token", "data", claude, codex); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(codex)
	if string(before) != string(after) {
		t.Fatal("declined replacement")
	}
}

func TestThreeEmptyAttempts(t *testing.T) {
	var out strings.Builder
	calls := 0
	u := setupUI{strings.NewReader(""), &out, func() ([]byte, error) { calls++; return nil, nil }}
	root := t.TempDir()
	_, e := acquire(context.Background(), aulaglobal.New("http://127.0.0.1:1", nil), filepath.Join(root, "secrets", "token"), filepath.Join(root, "data"), u)
	if e == nil || calls != 3 {
		t.Fatal(e, calls)
	}
}

func TestClaudeRegistration(t *testing.T) {
	for _, tc := range []struct {
		name, input, get         string
		getErr, addErr, fallback bool
		calls                    int
	}{
		{"first install", "\n", "No MCP server named \"aulaglobal\". Run `claude mcp add` to add one.", true, false, false, 2},
		{"replace", "s\ns\n", "Scope: User config (available in all your projects)", false, false, false, 3},
		{"keep", "s\nn\n", "Scope: User config (available in all your projects)", false, false, false, 1},
		{"project scope", "s\n", "Scope: Project config", false, false, false, 1},
		{"other scope", "s\n", "Scope: Local config", false, false, false, 1},
		{"legacy missing", "s\n", "NO MCP SERVER FOUND", true, false, false, 2},
		{"failure", "s\n", "No MCP server found", true, true, true, 2},
		{"unknown failure", "s\n", "offline", true, false, true, 1},
		{"decline", "n\n", "", false, false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			var calls [][]string
			run := func(ctx context.Context, path string, args ...string) ([]byte, error) {
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < 20*time.Second {
					t.Fatal("missing deadline")
				}
				calls = append(calls, append([]string{}, args...))
				if args[1] == "get" {
					if tc.getErr {
						return []byte(tc.get), fmt.Errorf("missing")
					}
					return []byte(tc.get), nil
				}
				if args[1] == "add" && tc.addErr {
					return []byte("private diagnostic"), fmt.Errorf("failure")
				}
				if args[1] == "add" {
					return []byte("Added stdio MCP server aulaglobal with command: /bin/true --transport stdio to user config"), nil
				}
				return []byte("Removed MCP server aulaglobal from user config"), nil
			}
			u := setupUI{strings.NewReader(tc.input), &out, nil}
			fallback := registerCode(u, "/cli", "/path with spaces/program", []string{"-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes"}, run)
			if fallback != tc.fallback || len(calls) != tc.calls {
				t.Fatal(fallback, calls, out.String())
			}
			for _, args := range calls {
				if args[1] == "add" {
					want := []string{"mcp", "add", "--scope", "user", "-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes", "aulaglobal", "--", "/path with spaces/program", "--transport", "stdio"}
					if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
						t.Fatal(args)
					}
				}
			}
			if fallback {
				manualCode(u, "/cli", "/program", nil)
				if !strings.Contains(out.String(), "Alternativa manual") {
					t.Fatal(out.String())
				}
			}
			if strings.Contains(out.String(), "private diagnostic") {
				t.Fatal("diagnostic leaked")
			}
		})
	}
	var out strings.Builder
	if !registerCode(setupUI{strings.NewReader(""), &out, nil}, "", "/program", nil, nil) {
		t.Fatal("missing fallback")
	}
	if strings.Join(codeArgs("/exe", nil), " ") != "mcp add --scope user aulaglobal -- /exe --transport stdio" {
		t.Fatal("default args")
	}
}

func TestFindClaude(t *testing.T) {
	// Fake metadata keeps simulated OS cases independent of the host's
	// executable-bit support. Candidate paths use the host's filepath separators.
	home, appdata := "HOME", "APPDATA"
	local := filepath.Join(home, ".local", "bin", "claude")
	for _, tc := range []struct {
		name, goos, candidate, fromPath, want string
		mode                                  os.FileMode
	}{
		{"PATH wins", "darwin", local, "/from/path", "/from/path", 0755},
		{"missing", "darwin", "", "", "", 0},
		{"darwin executable", "darwin", local, "", local, 0755},
		{"darwin non-executable", "darwin", local, "", "", 0644},
		{"darwin directory", "darwin", local, "", "", os.ModeDir | 0755},
		{"linux executable", "linux", local, "", local, 0755},
		{"windows home exe", "windows", filepath.Join(home, ".local", "bin", "claude.exe"), "", filepath.Join(home, ".local", "bin", "claude.exe"), 0644},
		{"windows home cmd", "windows", filepath.Join(home, ".local", "bin", "claude.cmd"), "", filepath.Join(home, ".local", "bin", "claude.cmd"), 0644},
		{"windows legacy home exe", "windows", filepath.Join(home, ".claude", "local", "claude.exe"), "", filepath.Join(home, ".claude", "local", "claude.exe"), 0644},
		{"windows APPDATA cmd", "windows", filepath.Join(appdata, "npm", "claude.cmd"), "", filepath.Join(appdata, "npm", "claude.cmd"), 0644},
		{"windows directory", "windows", filepath.Join(home, ".local", "bin", "claude.exe"), "", "", os.ModeDir | 0755},
	} {
		t.Run(tc.name, func(t *testing.T) {
			look := func(name string) (string, error) {
				if name != "claude" {
					t.Fatalf("PATH lookup name = %q, want claude", name)
				}
				if tc.fromPath != "" {
					return tc.fromPath, nil
				}
				return "", os.ErrNotExist
			}
			stat := func(path string) (os.FileInfo, error) {
				if tc.fromPath != "" {
					t.Fatalf("stat(%q) called after successful PATH lookup", path)
				}
				if path != tc.candidate || tc.candidate == "" {
					return nil, os.ErrNotExist
				}
				return fstest.MapFS{"candidate": &fstest.MapFile{Mode: tc.mode}}.Stat("candidate")
			}
			if got := findClaude(tc.goos, home, appdata, look, stat); got != tc.want {
				t.Fatalf("findClaude(%q) = %q, want %q (candidate %q, mode %v)", tc.goos, got, tc.want, tc.candidate, tc.mode)
			}
		})
	}
}

func TestSetupStepFormatting(t *testing.T) {
	var out strings.Builder
	u := setupUI{strings.NewReader(""), &out, nil}
	for i, title := range []string{"Carpetas", "Tu cuenta de AulaGlobal", "Comprobación", "Conectar con tus asistentes"} {
		u.step(i+1, title)
		fmt.Fprintln(u.out, "Una idea por línea.")
	}
	for i := 1; i <= 4; i++ {
		if !strings.Contains(out.String(), fmt.Sprintf("\n── Paso %d de 4", i)) {
			t.Fatal(out.String())
		}
	}
	if strings.Contains(out.String(), "\033") {
		t.Fatal("ANSI without TTY")
	}
	if strings.Count(out.String(), "──\n\n") != 4 {
		t.Fatal("missing separators")
	}
}
