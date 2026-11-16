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
	assistant := assistantstest.ByID(t, "claude-code")
	fake := &assistantstest.FakeClaude{Scope: "user", Command: "/x/aulaglobal-mcp", Env: map[string]string{"AULAGLOBAL_DOWNLOAD_DIR": "/notes"}}
	env := assistantstest.ClaudeEnv(t, fake)
	link := assistant.Find(env)
	if link.State != assistants.LinkPresent || link.Command != "/x/aulaglobal-mcp" || link.Env["AULAGLOBAL_DOWNLOAD_DIR"] != "/notes" || !strings.Contains(link.Detail, "claude mcp remove --scope user aulaglobal") {
		t.Fatalf("%+v", link)
	}
	if err := assistant.Remove(env); err != nil {
		t.Fatal(err)
	}
	if fake.Scope != "" {
		t.Fatal("entry remains")
	}
	last := fake.Calls[len(fake.Calls)-2]
	if strings.Join(last, " ") != "mcp remove --scope user aulaglobal" {
		t.Fatalf("calls %v", fake.Calls)
	}
	if link := assistant.Find(env); link.State != assistants.LinkNone {
		t.Fatalf("%+v", link)
	}
	n := len(fake.Calls)
	if err := assistant.Remove(env); err != nil || len(fake.Calls) != n+1 { // only the lookup
		t.Fatalf("second removal ran commands: %v", fake.Calls[n:])
	}
}

func TestClaudeCodeLocalAndProjectScopesAreNeverTouched(t *testing.T) {
	assistant := assistantstest.ByID(t, "claude-code")
	for _, scope := range []string{"local", "project"} {
		fake := &assistantstest.FakeClaude{Scope: scope, Command: "/x/aulaglobal-mcp"}
		env := assistantstest.ClaudeEnv(t, fake)
		link := assistant.Find(env)
		if link.State != assistants.LinkElsewhere || link.Detail == "" {
			t.Fatalf("%s: %+v", scope, link)
		}
		if scope == "project" && !strings.Contains(link.Detail, ".mcp.json") || scope == "local" && !strings.Contains(link.Detail, "--scope local") {
			t.Fatal(link.Detail)
		}
		if err := assistant.Remove(env); err == nil {
			t.Fatal("removed a non-user scope")
		}
		for _, c := range fake.Calls {
			if c[1] == "remove" {
				t.Fatalf("%s: remove was called: %v", scope, c)
			}
		}
	}
}

func TestClaudeCodeRemoveFailureAndUnknown(t *testing.T) {
	assistant := assistantstest.ByID(t, "claude-code")
	fake := &assistantstest.FakeClaude{Scope: "user", Command: "c", FailRemove: "private diagnostic"}
	if err := assistant.Remove(assistantstest.ClaudeEnv(t, fake)); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("%v", err)
	}
	// The entry vanished between get and remove: not an error.
	fake = &assistantstest.FakeClaude{Scope: "user", Command: "c", FailRemove: "No MCP server found with name: aulaglobal in user config"}
	env := assistantstest.ClaudeEnv(t, fake)
	env.RunClaude = func(ctx context.Context, cli string, args ...string) ([]byte, error) {
		if args[1] == "remove" {
			fake.Scope = ""
		}
		return fake.Run(ctx, cli, args...)
	}
	if err := assistant.Remove(env); err != nil {
		t.Fatal(err)
	}
	// Unknown scope and offline CLI are errors, never guesses.
	env.RunClaude = func(context.Context, string, ...string) ([]byte, error) { return []byte("Scope: Weird"), nil }
	if link := assistant.Find(env); link.State != assistants.LinkError {
		t.Fatalf("%+v", link)
	}
	env.RunClaude = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("private diagnostic"), errors.New("x")
	}
	if link := assistant.Find(env); link.State != assistants.LinkError || strings.Contains(link.Detail, "private") {
		t.Fatalf("%+v", link)
	}
	env.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	if link := assistant.Find(env); link.State != assistants.LinkNotInstalled {
		t.Fatalf("%+v", link)
	}
}
