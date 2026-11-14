package main

import (
	"aulaglobal-mcp/internal/userpaths"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeJSONCAndEnvironment(t *testing.T) {
	e, w, _, _ := clientFixture(t, openCodeAssistant{}, true)
	dir := userpaths.OpenCode(e.Home, e.XDG)
	path := filepath.Join(dir, "opencode.jsonc")
	old := []byte("{ // keep\n}")
	if e := os.WriteFile(path, old, 0600); e != nil {
		t.Fatal(e)
	}
	var out strings.Builder
	configureClients(setupUI{strings.NewReader("\n"), &out, nil}, e, w, []assistant{openCodeAssistant{}}, false)
	b, _ := os.ReadFile(path)
	if string(b) != string(old) || !strings.Contains(out.String(), "Alternativa manual") {
		t.Fatal(out.String())
	}
	entry := openCodeEntry("/server", []string{"-e", "AULAGLOBAL_TOKEN_FILE=/private/token", "-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes"})
	env := entry["environment"].(map[string]string)
	if env["AULAGLOBAL_TOKEN_FILE"] != "/private/token" || env["AULAGLOBAL_DOWNLOAD_DIR"] != "/notes" {
		t.Fatal(entry)
	}
}

func TestFindOpenCode(t *testing.T) {
	missing := func(string) (string, error) { return "", errors.New("missing") }
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			// Fake metadata: simulated OS must never depend on host executable bits.
			noStat := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
			if findOpenCode(goos, "home", "config", missing, noStat) != "" {
				t.Fatal("missing")
			}
			if findOpenCode(goos, "home", "config", func(string) (string, error) { return "binary", nil }, noStat) == "" {
				t.Fatal("PATH")
			}
			name := "opencode"
			mode := os.FileMode(0700)
			if goos == "windows" {
				name += ".exe"
				mode = 0600
			}
			target := filepath.Join("home", ".opencode", "bin", name)
			fake := func(p string) (os.FileInfo, error) {
				if p == target {
					return openCodeFileInfo{mode}, nil
				}
				return nil, os.ErrNotExist
			}
			if findOpenCode(goos, "home", "config", missing, fake) == "" {
				t.Fatal("common install")
			}
		})
	}
}

type openCodeFileInfo struct{ mode os.FileMode }

func (f openCodeFileInfo) Name() string       { return "opencode" }
func (f openCodeFileInfo) Size() int64        { return 0 }
func (f openCodeFileInfo) Mode() os.FileMode  { return f.mode }
func (f openCodeFileInfo) ModTime() time.Time { return time.Time{} }
func (f openCodeFileInfo) IsDir() bool        { return false }
func (f openCodeFileInfo) Sys() any           { return nil }

func TestOpenCodeV2PreservesContent(t *testing.T) {
	old := []byte(`{"custom":{"keep":true},"mcp":{"settings":"keep","servers":{"other":{"type":"local","command":["other"]}}}}`)
	b, _, err := openCodeConfig(old, openCodeEntry("/exe", nil), true)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	json.Unmarshal(b, &root)
	m := root["mcp"].(map[string]any)
	if root["custom"] == nil || m["settings"] != "keep" || m["servers"].(map[string]any)["other"] == nil || strings.Contains(string(b), "enabled") {
		t.Fatal(string(b))
	}
}
func TestOpenCodeWindowsNPM(t *testing.T) {
	target := filepath.Join("profile", "npm", "opencode.cmd")
	got := findOpenCode("windows", "home", "profile", func(string) (string, error) { return "", os.ErrNotExist }, func(p string) (os.FileInfo, error) {
		if p == target {
			return openCodeFileInfo{0600}, nil
		}
		return nil, os.ErrNotExist
	})
	if got != target {
		t.Fatal(got)
	}
}
