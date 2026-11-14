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
