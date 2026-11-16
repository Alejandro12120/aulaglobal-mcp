package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

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

func TestOtherClaudeScopes(t *testing.T) {
	for _, scope := range []string{"local", "project"} {
		for _, answer := range []string{"\n", "n\n"} {
			t.Run(scope+answer, func(t *testing.T) {
				a := claudeAssistant{}
				e, w, state, _ := clientFixture(t, a, true)
				*state = scope
				var out strings.Builder
				configureClients(setupUI{strings.NewReader(answer), &out, nil}, e, w, []assistant{a}, false)
				want := "actualizado"
				if answer == "n\n" {
					want = "omitido"
					if *state != scope {
						t.Fatal("changed after decline")
					}
				}
				if !strings.Contains(out.String(), want) {
					t.Fatal(out.String())
				}
			})
		}
	}
}

func TestClaudeUnknownInspection(t *testing.T) {
	for _, tc := range []struct {
		output string
		err    error
	}{{"Scope: Unknown config", nil}, {"private diagnostic", errors.New("offline")}} {
		a := claudeAssistant{}
		e, w, _, _ := clientFixture(t, a, true)
		calls := 0
		e.Run = func(context.Context, string, ...string) ([]byte, error) { calls++; return []byte(tc.output), tc.err }
		var out strings.Builder
		configureClients(setupUI{strings.NewReader(""), &out, nil}, e, w, []assistant{a}, true)
		if calls != 1 || !strings.Contains(out.String(), "✗") || strings.Contains(out.String(), "private diagnostic") {
			t.Fatal(out.String(), calls)
		}
	}
}

func TestClientCLIArgumentsAndManual(t *testing.T) {
	want := []string{"mcp", "add", "--scope", "user", "aulaglobal", "-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes space", "--", "/server", "--transport", "stdio"}
	if !sameEntry(codeArgs("/server", []string{"-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes space"}), want) {
		t.Fatal("Claude variadic env consumed the name")
	}
	extra := []string{"-e", "SECRET=never", "-e", "AULAGLOBAL_TOKEN_FILE=/token"}
	if strings.Contains(strings.Join(openCodeArgs("/exe", extra), " "), "never") {
		t.Fatal("unapproved env")
	}
	var out strings.Builder
	manualCode(setupUI{out: &out}, "claude", "/path with spaces/server", []string{"-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes"})
	for _, part := range []string{"'--scope' 'user'", "'/path with spaces/server'", "'AULAGLOBAL_DOWNLOAD_DIR=/notes'"} {
		if !strings.Contains(out.String(), part) {
			t.Fatal(out.String())
		}
	}
}

// fakeClaudeCLI imitates `claude mcp get/remove` with the real CLI's messages.
type fakeClaudeCLI struct {
	scope, command string // "" = no entry
	env            map[string]string
	calls          [][]string
	failRemove     string // output of a failing remove
}

func (f *fakeClaudeCLI) run(_ context.Context, _ string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	switch args[1] {
	case "get":
		if f.scope == "" {
			return []byte("No MCP server found with name: \"aulaglobal\"."), errors.New("exit 1")
		}
		scope := map[string]string{"user": "User config (available in all your projects)", "local": "Local config (private to you in this project)", "project": "Project config (shared via .mcp.json)"}[f.scope]
		var env strings.Builder
		for k, v := range f.env {
			env.WriteString(fmt.Sprintf("    %s=%s\n", k, v))
		}
		return []byte(fmt.Sprintf("aulaglobal:\n  Scope: %s\n  Status: ✓ Connected\n  Type: stdio\n  Command: %s\n  Args: --transport stdio\n  Environment:\n%s\nTo remove this server, run: claude mcp remove \"aulaglobal\" -s %s\n", scope, f.command, env.String(), f.scope)), nil
	case "remove":
		if f.failRemove != "" {
			return []byte(f.failRemove), errors.New("exit 1")
		}
		if f.scope != "user" || args[2] != "--scope" || args[3] != "user" || args[4] != "aulaglobal" {
			return []byte("No MCP server found with name: \"aulaglobal\" in user config"), errors.New("exit 1")
		}
		f.scope = ""
		return []byte("Removed MCP server aulaglobal from user config"), nil
	}
	return nil, errors.New("unexpected")
}

func claudeEnv(t *testing.T, f *fakeClaudeCLI) clientEnv {
	e := fileClientEnv(t)
	e.Look = func(n string) (string, error) {
		if n == "claude" {
			return "fake-claude", nil
		}
		return "", os.ErrNotExist
	}
	e.Run = f.run
	return e
}

func TestClaudeCodeFindAndRemove(t *testing.T) {
	a := claudeAssistant{}
	f := &fakeClaudeCLI{scope: "user", command: "/x/aulaglobal-mcp", env: map[string]string{"AULAGLOBAL_DOWNLOAD_DIR": "/notes"}}
	e := claudeEnv(t, f)
	l := a.Find(e)
	if l.state != linkPresent || l.command != "/x/aulaglobal-mcp" || l.env["AULAGLOBAL_DOWNLOAD_DIR"] != "/notes" || !strings.Contains(l.detail, "claude mcp remove --scope user aulaglobal") {
		t.Fatalf("%+v", l)
	}
	if err := a.Remove(e); err != nil {
		t.Fatal(err)
	}
	if f.scope != "" {
		t.Fatal("entry remains")
	}
	last := f.calls[len(f.calls)-2]
	if strings.Join(last, " ") != "mcp remove --scope user aulaglobal" {
		t.Fatalf("calls %v", f.calls)
	}
	if l := a.Find(e); l.state != linkNone {
		t.Fatalf("%+v", l)
	}
	n := len(f.calls)
	if err := a.Remove(e); err != nil || len(f.calls) != n+1 { // only the lookup
		t.Fatalf("second removal ran commands: %v", f.calls[n:])
	}
}

func TestClaudeCodeLocalAndProjectScopesAreNeverTouched(t *testing.T) {
	a := claudeAssistant{}
	for _, scope := range []string{"local", "project"} {
		f := &fakeClaudeCLI{scope: scope, command: "/x/aulaglobal-mcp"}
		e := claudeEnv(t, f)
		l := a.Find(e)
		if l.state != linkElsewhere || l.detail == "" {
			t.Fatalf("%s: %+v", scope, l)
		}
		if scope == "project" && !strings.Contains(l.detail, ".mcp.json") || scope == "local" && !strings.Contains(l.detail, "--scope local") {
			t.Fatal(l.detail)
		}
		if err := a.Remove(e); err == nil {
			t.Fatal("removed a non-user scope")
		}
		for _, c := range f.calls {
			if c[1] == "remove" {
				t.Fatalf("%s: remove was called: %v", scope, c)
			}
		}
	}
}

func TestClaudeCodeRemoveFailureAndUnknown(t *testing.T) {
	a := claudeAssistant{}
	f := &fakeClaudeCLI{scope: "user", command: "c", failRemove: "private diagnostic"}
	if err := a.Remove(claudeEnv(t, f)); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("%v", err)
	}
	// The entry vanished between get and remove: not an error.
	f = &fakeClaudeCLI{scope: "user", command: "c", failRemove: "No MCP server found with name: aulaglobal in user config"}
	e := claudeEnv(t, f)
	e.Run = func(ctx context.Context, cli string, args ...string) ([]byte, error) {
		if args[1] == "remove" {
			f.scope = ""
		}
		return f.run(ctx, cli, args...)
	}
	if err := a.Remove(e); err != nil {
		t.Fatal(err)
	}
	// Unknown scope and offline CLI are errors, never guesses.
	e.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte("Scope: Weird"), nil }
	if l := a.Find(e); l.state != linkError {
		t.Fatalf("%+v", l)
	}
	e.Run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("private diagnostic"), errors.New("x")
	}
	if l := a.Find(e); l.state != linkError || strings.Contains(l.detail, "private") {
		t.Fatalf("%+v", l)
	}
	e.Look = func(string) (string, error) { return "", os.ErrNotExist }
	if l := a.Find(e); l.state != linkNotInstalled {
		t.Fatalf("%+v", l)
	}
}
