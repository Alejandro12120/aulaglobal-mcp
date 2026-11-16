package main

import (
	"aulaglobal-mcp/internal/userpaths"
	"context"
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
func TestOpenCodeCapabilities(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			for _, mode := range []string{"scriptable", "legacy", "failure", "unverified", "both files"} {
				t.Run(mode, func(t *testing.T) {
					a := openCodeAssistant{}
					e, w, _, _ := clientFixture(t, a, true)
					e.GOOS = goos
					p, _ := a.path(e)
					calls := 0
					e.Look = func(n string) (string, error) {
						if (goos == "windows" && n == "opencode.exe") || (goos != "windows" && n == "opencode") {
							return "fake-opencode", nil
						}
						return "", os.ErrNotExist
					}
					if mode == "both files" {
						os.WriteFile(p, []byte(`{}`), 0600)
						os.WriteFile(p+"c", []byte(`{}`), 0600)
					}
					e.OpenRun = func(ctx context.Context, cli string, args ...string) ([]byte, error) {
						if _, ok := ctx.Deadline(); !ok {
							t.Fatal("no deadline")
						}
						calls++
						if cli != "fake-opencode" {
							t.Fatalf("unexpected CLI %q", cli)
						}
						if args[len(args)-1] == "--help" {
							if mode == "legacy" {
								return os.ReadFile("testdata/opencode-add-help.txt")
							}
							return []byte("--global\r\n--env\r\n"), nil
						}
						if mode == "failure" {
							return nil, errors.New("private diagnostic")
						}
						if !sameEntry(args, openCodeArgs(w.Exe, w.Extra)) {
							t.Fatal(args)
						}
						if mode != "unverified" {
							b, _, _ := openCodeConfig(nil, openCodeEntry(w.Exe, w.Extra), true)
							os.WriteFile(p, b, 0600)
						}
						return nil, nil
					}
					var out strings.Builder
					configureClients(setupUI{strings.NewReader(""), &out, nil}, e, w, []assistant{a}, true)
					failure := mode == "failure" || mode == "unverified" || mode == "both files"
					if strings.Contains(out.String(), "✗") != failure {
						t.Fatal(out.String())
					}
					if mode == "both files" && calls != 0 {
						t.Fatal("called CLI despite conflicting files")
					}
					if mode != "both files" {
						wantCalls := 2 // Capability probe followed by registration.
						if mode == "legacy" {
							wantCalls = 1 // Unsupported CLI uses the config writer.
						}
						if calls != wantCalls {
							t.Fatalf("CLI calls = %d, want %d", calls, wantCalls)
						}
					}
					if mode == "failure" || mode == "unverified" {
						if _, err := os.Stat(p); !os.IsNotExist(err) {
							t.Fatalf("config fallback after CLI failure: %v", err)
						}
					}
				})
			}
		})
	}
}

func TestOpenCodeConflictsAndV1(t *testing.T) {
	a := openCodeAssistant{}
	e, w, _, _ := clientFixture(t, a, true)
	p, _ := a.path(e)
	before := []byte(`{"theme":"keep","mcp":{"other":{"type":"local","command":["keep"]},"aulaglobal":{"type":"local","command":["old"]}}}`)
	os.WriteFile(p, before, 0600)
	if err := a.Apply(e, w, true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	if !strings.Contains(string(after), `"enabled": true`) || !strings.Contains(string(after), `"other"`) {
		t.Fatal(string(after))
	}
	backup, _ := os.ReadFile(p + ".bak")
	if string(backup) != string(before) {
		t.Fatal("backup changed")
	}
	if err := os.WriteFile(p+"c", []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(e, w, true); err == nil {
		t.Fatal("ambiguous files accepted")
	}
	unchanged, _ := os.ReadFile(p)
	if string(after) != string(unchanged) {
		t.Fatal("changed on conflict")
	}
}
