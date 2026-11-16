package assistants_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/assistants/assistantstest"
)

func TestOpenCodeEnvironment(t *testing.T) {
	entry := assistants.OpenCodeEntry("/server", []string{"-e", "AULAGLOBAL_TOKEN_FILE=/private/token", "-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes"})
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
			if assistants.FindOpenCode(goos, "home", "config", missing, noStat) != "" {
				t.Fatal("missing")
			}
			if assistants.FindOpenCode(goos, "home", "config", func(string) (string, error) { return "binary", nil }, noStat) == "" {
				t.Fatal("PATH")
			}
			name := "opencode"
			mode := os.FileMode(0700)
			if goos == "windows" {
				name += ".exe"
				mode = 0600
			}
			target := filepath.Join("home", ".opencode", "bin", name)
			fake := func(configPath string) (os.FileInfo, error) {
				if configPath == target {
					return openCodeFileInfo{mode}, nil
				}
				return nil, os.ErrNotExist
			}
			if assistants.FindOpenCode(goos, "home", "config", missing, fake) == "" {
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
	b, _, err := assistants.OpenCodeConfig(old, assistants.OpenCodeEntry("/exe", nil), true)
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
	got := assistants.FindOpenCode("windows", "home", "profile", func(string) (string, error) { return "", os.ErrNotExist }, func(configPath string) (os.FileInfo, error) {
		if configPath == target {
			return openCodeFileInfo{0600}, nil
		}
		return nil, os.ErrNotExist
	})
	if got != target {
		t.Fatal(got)
	}
}

func TestOpenCodeConflictsAndV1(t *testing.T) {
	assistant := assistantstest.ByID(t, "opencode")
	env, entry, _, _ := assistantstest.Fixture(t, assistant, true)
	configPath := assistantstest.ConfigPath(assistant, env)
	before := []byte(`{"theme":"keep","mcp":{"other":{"type":"local","command":["keep"]},"aulaglobal":{"type":"local","command":["old"]}}}`)
	os.WriteFile(configPath, before, 0600)
	if err := assistant.Apply(env, entry, true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(configPath)
	if !strings.Contains(string(after), `"enabled": true`) || !strings.Contains(string(after), `"other"`) {
		t.Fatal(string(after))
	}
	backup, _ := os.ReadFile(configPath + ".bak")
	if string(backup) != string(before) {
		t.Fatal("backup changed")
	}
	if err := os.WriteFile(configPath+"c", []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := assistant.Apply(env, entry, true); err == nil {
		t.Fatal("ambiguous files accepted")
	}
	unchanged, _ := os.ReadFile(configPath)
	if string(after) != string(unchanged) {
		t.Fatal("changed on conflict")
	}
}

func TestOpenCodeCapabilities(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			for _, mode := range []string{"scriptable", "legacy", "failure", "unverified", "both files"} {
				t.Run(mode, func(t *testing.T) {
					assistant := assistantstest.ByID(t, "opencode")
					env, entry, _, _ := assistantstest.Fixture(t, assistant, true)
					env.GOOS = goos
					configPath := assistantstest.ConfigPath(assistant, env)
					calls := 0
					env.LookPath = func(n string) (string, error) {
						if (goos == "windows" && n == "opencode.exe") || (goos != "windows" && n == "opencode") {
							return "fake-opencode", nil
						}
						return "", os.ErrNotExist
					}
					if mode == "both files" {
						os.WriteFile(configPath, []byte(`{}`), 0600)
						os.WriteFile(configPath+"c", []byte(`{}`), 0600)
					}
					env.RunOpenCode = func(ctx context.Context, cli string, args ...string) ([]byte, error) {
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
						if !assistants.SameEntry(args, assistants.OpenCodeArgs(entry.Exe, entry.Extra)) {
							t.Fatal(args)
						}
						if mode != "unverified" {
							b, _, _ := assistants.OpenCodeConfig(nil, assistants.OpenCodeEntry(entry.Exe, entry.Extra), true)
							os.WriteFile(configPath, b, 0600)
						}
						return nil, nil
					}
					err := configure(assistant, env, entry)
					failure := mode == "failure" || mode == "unverified" || mode == "both files"
					if (err != nil) != failure {
						t.Fatal("failure =", failure, "error:", err)
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
						if _, err := os.Stat(configPath); !os.IsNotExist(err) {
							t.Fatalf("config fallback after CLI failure: %v", err)
						}
					}
				})
			}
		})
	}
}

// configure does what setup does for one assistant when the user answers yes
// to everything: inspect it, then add or replace the entry.
func configure(assistant assistants.Assistant, env assistants.Env, entry assistants.Entry) error {
	inspection := assistant.Inspect(env, entry)
	if inspection.Err != nil {
		return inspection.Err
	}
	if inspection.State == assistants.Identical {
		return nil
	}
	return assistant.Apply(env, entry, inspection.State != assistants.Missing)
}
