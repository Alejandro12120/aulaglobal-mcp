package main

import (
	"aulaglobal-mcp/internal/userpaths"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAntigravityRegistration(t *testing.T) {
	for _, tc := range []struct {
		name, old, input string
		changed          bool
	}{
		{"create", "", "\n", true},
		{"merge", `{"custom":1234567890123456789,"mcpServers":{"remote":{"serverUrl":"https://example.com","disabled":true}}}`, "\n", true},
		{"replace", `{"mcpServers":{"aulaglobal":{"command":"old"}}}`, "s\ns\n", true},
		{"keep", `{"mcpServers":{"aulaglobal":{}}}`, "s\nn\n", false},
		{"decline", "", "n\n", false},
		{"comments", "{ // secret\n}", "\n", false},
		{"comma", `{"private":"secret",}`, "\n", false},
		{"duplicate", `{"private":1,"private":2}`, "\n", false},
		{"null", `null`, "\n", false},
		{"null servers", `{"mcpServers":null}`, "\n", false},
		{"array", `[]`, "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp_config.json")
			if tc.old != "" {
				if e := os.WriteFile(path, []byte(tc.old), 0600); e != nil {
					t.Fatal(e)
				}
			}
			var out strings.Builder
			u := setupUI{strings.NewReader(tc.input), &out, nil}
			u.step(4, "Conectar con tus asistentes")
			registerAntigravity(u, true, "", path, "/server", nil)
			b, _ := os.ReadFile(path)
			bak, be := os.ReadFile(path + ".bak")
			if tc.changed {
				if !strings.Contains(string(b), `"command": "/server"`) || strings.Contains(string(b), `"env"`) {
					t.Fatal(string(b))
				}
				if tc.old != "" && (be != nil || string(bak) != tc.old) {
					t.Fatal("backup")
				}
				if tc.name == "merge" && (!strings.Contains(string(b), "1234567890123456789") || !strings.Contains(string(b), "serverUrl")) {
					t.Fatal(string(b))
				}
			} else {
				if string(b) != tc.old || !os.IsNotExist(be) {
					t.Fatal("changed")
				}
				if tc.input == "\n" && !strings.Contains(out.String(), "Alternativa manual") {
					t.Fatal(out.String())
				}
			}
			if !strings.Contains(out.String(), "✓ Antigravity: detectado.") || !strings.Contains(out.String(), "› ¿Añadir AulaGlobal a Antigravity? [S/n]") || strings.Contains(out.String(), "secret") || strings.Contains(out.String(), "\033") {
				t.Fatal(out.String())
			}
		})
	}
	var out strings.Builder
	registerAntigravity(setupUI{strings.NewReader(""), &out, nil}, false, "", "", "", nil)
	if out.String() != "! Antigravity: no detectado.\n" {
		t.Fatal(out.String())
	}
	b, _, e := antigravityConfig(nil, map[string]any{"env": openCodeEntry("/server", []string{"-e", "AULAGLOBAL_TOKEN_FILE=/private/token", "-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes"})["environment"]}, true)
	if e != nil || !strings.Contains(string(b), "/private/token") || !strings.Contains(string(b), "/notes") {
		t.Fatal(string(b), e)
	}
}

func TestFindAntigravity(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		home, local := "HOME", "LOCAL"
		candidates := []struct {
			path string
			mode os.FileMode
		}{{filepath.Join(home, ".gemini", "antigravity"), os.ModeDir | 0700}, {filepath.Dir(userpaths.Antigravity(home)), os.ModeDir | 0700}}
		switch goos {
		case "linux":
			candidates = append(candidates, struct {
				path string
				mode os.FileMode
			}{"/usr/share/antigravity/antigravity", 0755})
		case "darwin":
			candidates = append(candidates, struct {
				path string
				mode os.FileMode
			}{"/Applications/Antigravity.app", os.ModeDir | 0755})
		case "windows":
			candidates = append(candidates, struct {
				path string
				mode os.FileMode
			}{filepath.Join(local, "Programs", "Antigravity", "Antigravity.exe"), 0644})
		}
		look := func(string) (string, error) { return "", os.ErrNotExist }
		for _, c := range candidates {
			stat := func(p string) (os.FileInfo, error) {
				if p != c.path {
					return nil, os.ErrNotExist
				}
				return fstest.MapFS{"x": &fstest.MapFile{Mode: c.mode}}.Stat("x")
			}
			if _, ok := findAntigravity(goos, home, local, look, stat); !ok {
				t.Fatal(goos, c)
			}
		}
		missing := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
		if _, ok := findAntigravity(goos, home, local, look, missing); ok {
			t.Fatal("present")
		}
		cli, ok := findAntigravity(goos, home, local, func(n string) (string, error) {
			if n == "agy" {
				return "fake-cli", nil
			}
			return "", os.ErrNotExist
		}, missing)
		if !ok || cli != "fake-cli" {
			t.Fatal(cli, ok)
		}
	}
}

func TestRealAntigravity(t *testing.T) {
	cli := os.Getenv("AULAGLOBAL_TEST_AGY")
	if cli == "" {
		t.Skip("set AULAGLOBAL_TEST_AGY to isolated binary")
	}
	entry, e := antigravityCLIEntry(cli, "/bin/true", []string{"-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes"})
	if e != nil || entry["command"] != "/bin/true" || entry["disabled"] != false {
		t.Fatal(entry, e)
	}
	t.Logf("real CLI generated entry: %v", entry)
}

func TestAntigravityHelpFixture(t *testing.T) {
	b, e := os.ReadFile("testdata/agy-1.5.9-mcp-add-help.txt")
	if e != nil || !strings.Contains(string(b), "<commandOrUrl>") || !strings.Contains(string(b), "--env") {
		t.Fatal("unexpected real CLI help", e)
	}
}
