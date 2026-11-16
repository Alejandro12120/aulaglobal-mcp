// Package assistantstest has fakes for testing code that uses the assistants
// package: an Env rooted in a temporary home, a fake Claude Code CLI and a
// fixture that gives every assistant the same scripted machine.
package assistantstest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/userpaths"
)

// FileEnv is an Env rooted in a temporary home: no real file, PATH or process
// is reachable from it, and no assistant is found.
func FileEnv(t *testing.T) assistants.Env {
	t.Helper()
	home := t.TempDir()
	env := assistants.Env{GOOS: runtime.GOOS, Home: home, ConfigDir: filepath.Join(home, "config")}
	env.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	env.Stat = func(path string) (os.FileInfo, error) {
		if !strings.HasPrefix(path, home+string(os.PathSeparator)) {
			return nil, os.ErrNotExist
		}
		return os.Stat(path)
	}
	return env
}

// ByID returns the assistant with the given ID.
func ByID(t *testing.T, id string) assistants.Assistant {
	t.Helper()
	selected, err := assistants.Select(id)
	if err != nil || len(selected) != 1 {
		t.Fatal(id, err)
	}
	return selected[0]
}

// ConfigPath is where an assistant that keeps its configuration in one file
// stores it. It panics for Claude Code, which has no such file.
func ConfigPath(assistant assistants.Assistant, env assistants.Env) string {
	switch assistant.ID() {
	case "claude-desktop":
		return userpaths.Claude(env.GOOS, env.Home, env.ConfigDir)
	case "codex":
		return filepath.Join(env.Home, ".codex", "config.toml")
	case "opencode":
		return filepath.Join(userpaths.OpenCode(env.Home, env.XDGConfigHome), "opencode.json")
	case "antigravity":
		return userpaths.Antigravity(env.Home)
	}
	panic("no configuration file for " + assistant.ID())
}

// Fixture gives every registered assistant the same scripted machine, with
// isolated files, fake executable metadata and subprocesses. When present is
// true the assistant is installed. The returned state is the aulaglobal entry
// of the fake Claude Code CLI: "missing", "identical", "different", "local" or
// "project". Setting the returned fail makes every CLI change fail with a
// private diagnostic.
func Fixture(t *testing.T, assistant assistants.Assistant, present bool) (env assistants.Env, entry assistants.Entry, state *string, fail *bool) {
	t.Helper()
	env = FileEnv(t)
	home := env.Home
	env.LookPath = func(name string) (string, error) {
		if present && assistant.ID() == "claude-code" && name == "claude" {
			return "fake-claude", nil
		}
		return "", os.ErrNotExist
	}
	if present {
		var dir string
		switch assistant.ID() {
		case "claude-desktop", "codex", "opencode", "antigravity":
			dir = filepath.Dir(ConfigPath(assistant, env))
		}
		if dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	entry, err := assistants.NewEntry(filepath.Join(home, "server"), filepath.Join(home, "private", "token"), filepath.Join(home, "notes"), "default-token", "default-data")
	if err != nil {
		t.Fatal(err)
	}
	claudeState := "missing"
	failChanges := false
	env.RunClaude = func(ctx context.Context, cli string, args ...string) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("no timeout")
		}
		if args[1] == "get" {
			if claudeState == "missing" {
				return []byte("No MCP server named \"aulaglobal\". Run `claude mcp add` to add one."), errors.New("missing")
			}
			scope := "User config (available in all your projects)"
			if claudeState == "local" {
				scope = "Local config (private to you in this project)"
			}
			if claudeState == "project" {
				scope = "Project config (shared via .mcp.json)"
			}
			command := entry.Exe
			if claudeState != "identical" {
				command = "old-server"
			}
			return []byte(fmt.Sprintf("aulaglobal:\n  Scope: %s\n  Type: stdio\n  Command: %s\n  Args: --transport stdio\n  Environment:\n    AULAGLOBAL_TOKEN_FILE=%s\n    AULAGLOBAL_DOWNLOAD_DIR=%s\n\nTo remove this server, run: claude mcp remove aulaglobal -s user", scope, command, filepath.Join(home, "private", "token"), filepath.Join(home, "notes"))), nil
		}
		if failChanges {
			return []byte("private diagnostic"), errors.New("private error")
		}
		if args[1] == "remove" {
			if claudeState == "project" {
				t.Fatal("project must not be removed")
			}
			if args[len(args)-1] != claudeState && claudeState != "different" {
				t.Fatal(args, claudeState)
			}
			claudeState = "missing"
		}
		if args[1] == "add" {
			claudeState = "identical"
		}
		return nil, nil
	}
	env.RunOpenCode = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("unsupported") }
	env.GenerateAntigravity = func(string, string, []string) (map[string]any, error) { return entry.Config(), nil }
	return env, entry, &claudeState, &failChanges
}

// Seed writes the configuration of a file based assistant as if setup had
// already run: with the wanted entry, or with an old one when state is
// "different". Claude Code has no file, so it does nothing there.
func Seed(t *testing.T, assistant assistants.Assistant, env assistants.Env, entry assistants.Entry, state string) {
	t.Helper()
	if assistant.ID() == "claude-code" {
		return
	}
	if state == "different" {
		entry.Exe += "-old"
	}
	env.WriteConfig = nil
	if err := assistant.Apply(env, entry, true); err != nil {
		t.Fatal(err)
	}
}

// FakeClaude imitates `claude mcp get` and `claude mcp remove` with the real
// CLI's messages.
type FakeClaude struct {
	Scope, Command string // an empty Scope means there is no entry
	Env            map[string]string
	Calls          [][]string
	FailRemove     string // output of a failing remove
}

// Run is the Runner of the fake CLI.
func (fake *FakeClaude) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	fake.Calls = append(fake.Calls, args)
	switch args[1] {
	case "get":
		if fake.Scope == "" {
			return []byte("No MCP server found with name: \"aulaglobal\"."), errors.New("exit 1")
		}
		scope := map[string]string{"user": "User config (available in all your projects)", "local": "Local config (private to you in this project)", "project": "Project config (shared via .mcp.json)"}[fake.Scope]
		var env strings.Builder
		for name, value := range fake.Env {
			env.WriteString(fmt.Sprintf("    %s=%s\n", name, value))
		}
		return []byte(fmt.Sprintf("aulaglobal:\n  Scope: %s\n  Status: ✓ Connected\n  Type: stdio\n  Command: %s\n  Args: --transport stdio\n  Environment:\n%s\nTo remove this server, run: claude mcp remove \"aulaglobal\" -s %s\n", scope, fake.Command, env.String(), fake.Scope)), nil
	case "remove":
		if fake.FailRemove != "" {
			return []byte(fake.FailRemove), errors.New("exit 1")
		}
		if fake.Scope != "user" || args[2] != "--scope" || args[3] != "user" || args[4] != "aulaglobal" {
			return []byte("No MCP server found with name: \"aulaglobal\" in user config"), errors.New("exit 1")
		}
		fake.Scope = ""
		return []byte("Removed MCP server aulaglobal from user config"), nil
	}
	return nil, errors.New("unexpected")
}

// ClaudeEnv is a FileEnv where Claude Code is installed and is the fake.
func ClaudeEnv(t *testing.T, fake *FakeClaude) assistants.Env {
	env := FileEnv(t)
	env.LookPath = func(name string) (string, error) {
		if name == "claude" {
			return "fake-claude", nil
		}
		return "", os.ErrNotExist
	}
	env.RunClaude = fake.Run
	return env
}
