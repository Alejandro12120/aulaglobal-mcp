package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenCodeRegistration(t *testing.T) {
	for _, tc := range []struct {
		name, old, input string
		changed, backup  bool
	}{
		{"create", "", "\n", true, false},
		{"merge", `{"model":"example","mcp":{"other":{"type":"local","command":["other"]}}}`, "s\n", true, true},
		{"replace no", `{"mcp":{"aulaglobal":{"command":["old"]}}}`, "s\nn\n", false, false},
		{"replace yes", `{"mcp":{"aulaglobal":{"command":["old"]}}}`, "s\ns\n", true, true},
		{"decline", "", "n\n", false, false},
		{"comments", "{ // private comment\n}", "\n", false, false},
		{"trailing comma", `{"model":"example",}`, "\n", false, false},
		{"null", `null`, "\n", false, false},
		{"bad mcp", `{"mcp":null}`, "\n", false, false},
		{"v2", `{"mcp":{"servers":{}}}`, "\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "opencode.json")
			if tc.old != "" {
				if e := os.WriteFile(path, []byte(tc.old), 0600); e != nil {
					t.Fatal(e)
				}
			}
			var out strings.Builder
			registerOpenCode(setupUI{strings.NewReader(tc.input), &out, nil}, true, dir, "/abs/server", nil)
			b, e := os.ReadFile(path)
			if tc.changed {
				if e != nil {
					t.Fatal(e)
				}
				var root map[string]any
				if e = json.Unmarshal(b, &root); e != nil {
					t.Fatal(e)
				}
				m := root["mcp"].(map[string]any)
				entry := m["aulaglobal"].(map[string]any)
				if entry["enabled"] != true || entry["environment"] != nil {
					t.Fatal(entry)
				}
				if tc.name == "merge" && (root["model"] != "example" || m["other"] == nil) {
					t.Fatal(root)
				}
				if tc.name == "create" && root["$schema"] == nil {
					t.Fatal(root)
				}
				if !strings.Contains(out.String(), "✓ Configuración de OpenCode") {
					t.Fatal(out.String())
				}
			} else if string(b) != tc.old {
				t.Fatal("changed", string(b))
			}
			bak, be := os.ReadFile(path + ".bak")
			if tc.backup {
				if be != nil || string(bak) != tc.old {
					t.Fatal("backup", be)
				}
			} else if !os.IsNotExist(be) {
				t.Fatal("unexpected backup")
			}
			if !strings.Contains(out.String(), "› ¿Añadir AulaGlobal a OpenCode? [S/n]") {
				t.Fatal(out.String())
			}
		})
	}
}

func TestOpenCodeJSONCAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.jsonc")
	old := []byte("{ // keep\n}")
	if e := os.WriteFile(path, old, 0600); e != nil {
		t.Fatal(e)
	}
	var out strings.Builder
	registerOpenCode(setupUI{strings.NewReader("\n"), &out, nil}, true, dir, "/server", nil)
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
			dir := t.TempDir()
			f, e := os.Stat(dir)
			if e != nil {
				t.Fatal(e)
			}
			stat := func(p string) (os.FileInfo, error) {
				if p == "config" {
					return f, nil
				}
				return nil, os.ErrNotExist
			}
			if !findOpenCode(goos, "home", "config", missing, stat) {
				t.Fatal("directory")
			}
			noStat := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
			if findOpenCode(goos, "home", "config", missing, noStat) {
				t.Fatal("missing")
			}
			if !findOpenCode(goos, "home", "config", func(string) (string, error) { return "binary", nil }, noStat) {
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
			if !findOpenCode(goos, "home", "config", missing, fake) {
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

func TestOpenCodeFileSelection(t *testing.T) {
	for _, both := range []bool{false, true} {
		t.Run(fmt.Sprint(both), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "opencode.jsonc")
			old := []byte(`{"model":"keep"}`)
			if e := os.WriteFile(path, old, 0600); e != nil {
				t.Fatal(e)
			}
			if both {
				if e := os.WriteFile(filepath.Join(dir, "opencode.json"), old, 0600); e != nil {
					t.Fatal(e)
				}
			}
			var out strings.Builder
			registerOpenCode(setupUI{strings.NewReader("\n"), &out, nil}, true, dir, "/server", nil)
			b, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			if both {
				if string(b) != string(old) || !strings.Contains(out.String(), "Alternativa manual") {
					t.Fatal(out.String())
				}
			} else {
				if !strings.Contains(string(b), "aulaglobal") {
					t.Fatal(string(b))
				}
				bak, e := os.ReadFile(path + ".bak")
				if e != nil || string(bak) != string(old) {
					t.Fatal("backup", e)
				}
			}
		})
	}
	var out strings.Builder
	registerOpenCode(setupUI{strings.NewReader(""), &out, nil}, false, t.TempDir(), "/server", nil)
	if out.String() != "! OpenCode: no detectado.\n" {
		t.Fatal(out.String())
	}
}
