package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"aulaglobal-mcp/internal/userpaths"
)

// Every registered implementation gets the same contract, with isolated files,
// fake executable metadata and subprocesses; no real HOME or service is used.
func clientFixture(t *testing.T, a assistant, present bool) (clientEnv, desiredEntry, *string, *bool) {
	t.Helper()
	e := fileClientEnv(t)
	home := e.Home
	e.Look = func(n string) (string, error) {
		if present && a.ID() == "claude-code" && n == "claude" {
			return "fake-claude", nil
		}
		return "", os.ErrNotExist
	}
	if present {
		var dir string
		switch a.ID() {
		case "claude-desktop":
			dir = filepath.Dir(userpaths.Claude(e.GOOS, e.Home, e.Config))
		case "codex":
			dir = filepath.Join(home, ".codex")
		case "opencode":
			dir = userpaths.OpenCode(home, "")
		case "antigravity":
			dir = filepath.Dir(userpaths.Antigravity(home))
		}
		if dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	w, err := desired(filepath.Join(home, "server"), filepath.Join(home, "private", "token"), filepath.Join(home, "notes"), "default-token", "default-data")
	if err != nil {
		t.Fatal(err)
	}
	state := "missing"
	fail := false
	e.Run = func(ctx context.Context, cli string, args ...string) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("no timeout")
		}
		if args[1] == "get" {
			if state == "missing" {
				return []byte("No MCP server named \"aulaglobal\". Run `claude mcp add` to add one."), errors.New("missing")
			}
			scope := "User config (available in all your projects)"
			if state == "local" {
				scope = "Local config (private to you in this project)"
			}
			if state == "project" {
				scope = "Project config (shared via .mcp.json)"
			}
			command := w.Exe
			if state != "identical" {
				command = "old-server"
			}
			return []byte(fmt.Sprintf("aulaglobal:\n  Scope: %s\n  Type: stdio\n  Command: %s\n  Args: --transport stdio\n  Environment:\n    AULAGLOBAL_TOKEN_FILE=%s\n    AULAGLOBAL_DOWNLOAD_DIR=%s\n\nTo remove this server, run: claude mcp remove aulaglobal -s user", scope, command, filepath.Join(home, "private", "token"), filepath.Join(home, "notes"))), nil
		}
		if fail {
			return []byte("private diagnostic"), errors.New("private error")
		}
		if args[1] == "remove" {
			if state == "project" {
				t.Fatal("project must not be removed")
			}
			if args[len(args)-1] != state && state != "different" {
				t.Fatal(args, state)
			}
			state = "missing"
		}
		if args[1] == "add" {
			state = "identical"
		}
		return nil, nil
	}
	e.OpenRun = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("unsupported") }
	e.GenerateAG = func(string, string, []string) (map[string]any, error) { return w.entry(), nil }
	return e, w, &state, &fail
}

func seedClient(t *testing.T, a assistant, e clientEnv, w desiredEntry, state string) {
	t.Helper()
	if a.ID() == "claude-code" {
		return
	}
	if state == "different" {
		w.Exe += "-old"
	}
	var p string
	var b []byte
	var err error
	switch a := a.(type) {
	case fileAssistant:
		p, err = a.path(e)
		if err == nil {
			b, _, err = a.merge(nil, w.entry(), true)
		}
	case openCodeAssistant:
		p, err = a.path(e)
		if err == nil {
			b, _, err = openCodeConfig(nil, openCodeEntry(w.Exe, w.Extra), true)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAssistantContracts(t *testing.T) {
	for _, a := range assistants {
		t.Run(a.ID(), func(t *testing.T) {
			for _, tc := range []struct {
				name, state, input, want string
				present, yes, fail       bool
			}{
				{name: "not detected", want: "No encontrados:", present: false},
				{"missing add", "missing", "\n", "añadido", true, false, false},
				{"missing skip", "missing", "n\n", "omitido", true, false, false},
				{"identical", "identical", "", "ya estaba", true, false, false},
				{"different update", "different", "\n", "actualizado", true, false, false},
				{"different skip", "different", "n\n", "omitido", true, false, false},
				{"yes", "different", "", "actualizado", true, true, false},
				{"failure", "missing", "\n", "✗", true, false, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					e, w, state, fail := clientFixture(t, a, tc.present)
					*state = tc.state
					if tc.present && (tc.state == "different" || tc.state == "identical") {
						seedClient(t, a, e, w, tc.state)
					}
					if tc.fail {
						*fail = true
						e.Write = func(string, []byte) error { return errors.New("write failure") }
					}
					var out strings.Builder
					configureClients(setupUI{strings.NewReader(tc.input), &out, nil}, e, w, []assistant{a}, tc.yes)
					s := out.String()
					if !strings.Contains(s, tc.want) {
						t.Fatal(s)
					}
					if tc.fail && (!strings.Contains(s, "Alternativa manual") || strings.Contains(s, "private diagnostic")) {
						t.Fatal(s)
					}
					if tc.state == "identical" && strings.Contains(s, "›") {
						t.Fatal("prompt for identical", s)
					}
					if tc.yes && strings.Contains(s, "›") {
						t.Fatal("prompt with --yes", s)
					}
					if (tc.want == "actualizado" || tc.want == "añadido") && a.Inspect(e, w).state != identical {
						t.Fatal("not identical after apply", a.Inspect(e, w))
					}
				})
			}
		})
	}
}

func TestClientSelection(t *testing.T) {
	for _, a := range assistants {
		got, err := selectAssistants(a.ID())
		if err != nil || len(got) != 1 || got[0].ID() != a.ID() {
			t.Fatal(got, err)
		}
	}
	if _, err := selectAssistants("bad"); err == nil {
		t.Fatal("invalid ID")
	}
	got, err := selectAssistants("codex,claude-code,codex")
	if err != nil || len(got) != 2 || got[0].ID() != "claude-code" {
		t.Fatal(got, err)
	}
}

func TestDesiredDefaults(t *testing.T) {
	w, err := desired("server", "token", "data", "token", "data")
	if err != nil || !filepath.IsAbs(w.Exe) || w.entry()["env"] != nil {
		t.Fatal(w, err)
	}
	w, err = desired("server", "private", "notes", "token", "data")
	if err != nil || len(w.entry()["env"].(map[string]string)) != 2 {
		t.Fatal(w, err)
	}
}

func TestNoHostMetadataForSimulatedOS(t *testing.T) {
	// Explicit fake directories also exercise Detect without host permission bits.
	for _, a := range assistants {
		e, _, _, _ := clientFixture(t, a, false)
		e.GOOS = "windows"
		e.Look = func(string) (string, error) { return "", os.ErrNotExist }
		e.Stat = func(string) (os.FileInfo, error) {
			return fstest.MapFS{"dir": &fstest.MapFile{Mode: os.ModeDir | 0600}}.Stat("dir")
		}
		if a.ID() != "claude-code" && !a.Detect(e) {
			t.Fatal(a.ID())
		}
	}
}

func TestUnsafeFilesAndBackups(t *testing.T) {
	for _, a := range assistants {
		if a.ID() == "claude-code" {
			continue
		}
		for _, bad := range []string{"{ // private\n}", `{"x":1,"x":2}`, `null`, `[]`, `{"mcpServers":null}`, `{"mcp":null}`} {
			if a.ID() == "codex" {
				bad = "[broken"
			}
			t.Run(a.ID()+bad, func(t *testing.T) {
				e, w, _, _ := clientFixture(t, a, true)
				var p string
				switch a := a.(type) {
				case fileAssistant:
					p, _ = a.path(e)
				case openCodeAssistant:
					p, _ = a.path(e)
				}
				// Skip shapes that are valid unrelated keys for this particular client.
				if a.ID() == "opencode" && bad == `{"mcpServers":null}` {
					return
				}
				if a.ID() != "opencode" && bad == `{"mcp":null}` {
					return
				}
				os.WriteFile(p, []byte(bad), 0600)
				var out strings.Builder
				configureClients(setupUI{strings.NewReader(""), &out, nil}, e, w, []assistant{a}, true)
				after, _ := os.ReadFile(p)
				if string(after) != bad || !strings.Contains(out.String(), "✗") {
					t.Fatal(out.String(), string(after))
				}
				if strings.Contains(out.String(), "// private") {
					t.Fatal("private content leaked")
				}
			})
		}
		t.Run(a.ID()+" backup", func(t *testing.T) {
			e, w, _, _ := clientFixture(t, a, true)
			seedClient(t, a, e, w, "different")
			var p string
			switch a := a.(type) {
			case fileAssistant:
				p, _ = a.path(e)
			case openCodeAssistant:
				p, _ = a.path(e)
			}
			before, _ := os.ReadFile(p)
			if err := a.Apply(e, w, true); err != nil {
				t.Fatal(err)
			}
			backup, err := os.ReadFile(p + ".bak")
			if err != nil || string(backup) != string(before) {
				t.Fatal("backup mismatch", err)
			}
		})
	}
}

// fileClientEnv is a clientEnv rooted in a temporary HOME: no real file, PATH or
// process is reachable from it.
func fileClientEnv(t *testing.T) clientEnv {
	t.Helper()
	home := t.TempDir()
	e := clientEnv{GOOS: runtime.GOOS, Home: home, Config: filepath.Join(home, "config")}
	e.Look = func(string) (string, error) { return "", os.ErrNotExist }
	e.Stat = func(p string) (os.FileInfo, error) {
		if !strings.HasPrefix(p, home+string(os.PathSeparator)) {
			return nil, os.ErrNotExist
		}
		return os.Stat(p)
	}
	return e
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
