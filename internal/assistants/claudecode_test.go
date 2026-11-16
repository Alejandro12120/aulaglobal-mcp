package assistants_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/assistants/assistantstest"
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
			if got := assistants.FindClaude(tc.goos, home, appdata, look, stat); got != tc.want {
				t.Fatalf("assistants.FindClaude(%q) = %q, want %q (candidate %q, mode %v)", tc.goos, got, tc.want, tc.candidate, tc.mode)
			}
		})
	}
}

func TestClientCLIArgumentsAndManual(t *testing.T) {
	want := []string{"mcp", "add", "--scope", "user", "aulaglobal", "-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes space", "--", "/server", "--transport", "stdio"}
	if !assistants.SameEntry(assistants.CodeArgs("/server", []string{"-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes space"}), want) {
		t.Fatal("Claude variadic env consumed the name")
	}
	extra := []string{"-e", "SECRET=never", "-e", "AULAGLOBAL_TOKEN_FILE=/token"}
	if strings.Contains(strings.Join(assistants.OpenCodeArgs("/exe", extra), " "), "never") {
		t.Fatal("unapproved env")
	}
	var out strings.Builder
	assistants.ManualCode(&out, "claude", "/path with spaces/server", []string{"-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes"})
	for _, part := range []string{"'--scope' 'user'", "'/path with spaces/server'", "'AULAGLOBAL_DOWNLOAD_DIR=/notes'"} {
		if !strings.Contains(out.String(), part) {
			t.Fatal(out.String())
		}
	}
}

func TestClaudeCodeFindAndRemove(t *testing.T) {
	a := assistantstest.ByID(t, "claude-code")
	f := &assistantstest.FakeClaude{Scope: "user", Command: "/x/aulaglobal-mcp", Env: map[string]string{"AULAGLOBAL_DOWNLOAD_DIR": "/notes"}}
	e := assistantstest.ClaudeEnv(t, f)
	l := a.Find(e)
	if l.State != assistants.LinkPresent || l.Command != "/x/aulaglobal-mcp" || l.Env["AULAGLOBAL_DOWNLOAD_DIR"] != "/notes" || !strings.Contains(l.Detail, "claude mcp remove --scope user aulaglobal") {
		t.Fatalf("%+v", l)
	}
	if err := a.Remove(e); err != nil {
		t.Fatal(err)
	}
	if f.Scope != "" {
		t.Fatal("entry remains")
	}
	last := f.Calls[len(f.Calls)-2]
	if strings.Join(last, " ") != "mcp remove --scope user aulaglobal" {
		t.Fatalf("calls %v", f.Calls)
	}
	if l := a.Find(e); l.State != assistants.LinkNone {
		t.Fatalf("%+v", l)
	}
	n := len(f.Calls)
	if err := a.Remove(e); err != nil || len(f.Calls) != n+1 { // only the lookup
		t.Fatalf("second removal ran commands: %v", f.Calls[n:])
	}
}

func TestClaudeCodeLocalAndProjectScopesAreNeverTouched(t *testing.T) {
	a := assistantstest.ByID(t, "claude-code")
	for _, scope := range []string{"local", "project"} {
		f := &assistantstest.FakeClaude{Scope: scope, Command: "/x/aulaglobal-mcp"}
		e := assistantstest.ClaudeEnv(t, f)
		l := a.Find(e)
		if l.State != assistants.LinkElsewhere || l.Detail == "" {
			t.Fatalf("%s: %+v", scope, l)
		}
		if scope == "project" && !strings.Contains(l.Detail, ".mcp.json") || scope == "local" && !strings.Contains(l.Detail, "--scope local") {
			t.Fatal(l.Detail)
		}
		if err := a.Remove(e); err == nil {
			t.Fatal("removed a non-user scope")
		}
		for _, c := range f.Calls {
			if c[1] == "remove" {
				t.Fatalf("%s: remove was called: %v", scope, c)
			}
		}
	}
}

func TestClaudeCodeRemoveFailureAndUnknown(t *testing.T) {
	a := assistantstest.ByID(t, "claude-code")
	f := &assistantstest.FakeClaude{Scope: "user", Command: "c", FailRemove: "private diagnostic"}
	if err := a.Remove(assistantstest.ClaudeEnv(t, f)); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("%v", err)
	}
	// The entry vanished between get and remove: not an error.
	f = &assistantstest.FakeClaude{Scope: "user", Command: "c", FailRemove: "No MCP server found with name: aulaglobal in user config"}
	e := assistantstest.ClaudeEnv(t, f)
	e.RunClaude = func(ctx context.Context, cli string, args ...string) ([]byte, error) {
		if args[1] == "remove" {
			f.Scope = ""
		}
		return f.Run(ctx, cli, args...)
	}
	if err := a.Remove(e); err != nil {
		t.Fatal(err)
	}
	// Unknown scope and offline CLI are errors, never guesses.
	e.RunClaude = func(context.Context, string, ...string) ([]byte, error) { return []byte("Scope: Weird"), nil }
	if l := a.Find(e); l.State != assistants.LinkError {
		t.Fatalf("%+v", l)
	}
	e.RunClaude = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("private diagnostic"), errors.New("x")
	}
	if l := a.Find(e); l.State != assistants.LinkError || strings.Contains(l.Detail, "private") {
		t.Fatalf("%+v", l)
	}
	e.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	if l := a.Find(e); l.State != assistants.LinkNotInstalled {
		t.Fatalf("%+v", l)
	}
}
