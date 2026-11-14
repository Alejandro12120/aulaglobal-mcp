package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Runs natively so the host's Claude installation is available. Never parallel:
// HOME, USERPROFILE, config overrides and the working directory are process-wide.
func TestRealClaudeRegistration(t *testing.T) {
	if os.Getenv("AULAGLOBAL_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set AULAGLOBAL_TEST_REAL_CLAUDE=1")
	}
	cli, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not on PATH")
	}
	cli, err = filepath.Abs(cli)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Chdir(t.TempDir())
	safe := func() {
		t.Helper()
		if os.Getenv("HOME") != home || os.Getenv("USERPROFILE") != home || home == "" || home == "/" || !strings.HasPrefix(home, os.TempDir()+string(os.PathSeparator)) {
			t.Fatal("refusing to run Claude outside temporary HOME")
		}
	}
	call := func(args ...string) (string, error) {
		t.Helper()
		safe()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := runClaude(ctx, cli, args...)
		t.Logf("claude %s: %s (error: %v)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
		return string(out), err
	}
	// Cleanup also runs on assertion failure, within the isolated environment.
	defer func() { call("mcp", "remove", "aulaglobal", "--scope", "user") }()
	for _, input := range []string{"\n", "s\ns\n"} {
		var ui strings.Builder
		var operations []string
		runner := func(ctx context.Context, path string, args ...string) ([]byte, error) {
			safe()
			operations = append(operations, args[1])
			out, err := runClaude(ctx, path, args...)
			t.Logf("%s: %s (error: %v)", args[1], strings.TrimSpace(string(out)), err)
			return out, err
		}
		if registerCode(setupUI{strings.NewReader(input), &ui, nil}, cli, "/bin/true", nil, runner) || !strings.Contains(ui.String(), "añadido") {
			t.Fatal(ui.String())
		}
		want := "get,add"
		if input != "\n" {
			want = "get,remove,add"
		}
		if strings.Join(operations, ",") != want {
			t.Fatalf("operations: %v", operations)
		}
		out, err := call("mcp", "get", "aulaglobal")
		if err != nil || !strings.Contains(out, "Scope: User config (available in all your projects)") {
			t.Fatalf("get: %s, %v", out, err)
		}
	}
	out, err := call("mcp", "remove", "aulaglobal", "--scope", "user")
	if err != nil || !strings.Contains(out, "Removed MCP server aulaglobal from user config") {
		t.Fatalf("remove: %s, %v", out, err)
	}
	out, err = call("mcp", "get", "aulaglobal")
	if err == nil || !strings.Contains(strings.ToLower(out), "no mcp server named") {
		t.Fatalf("get after removal: %s, %v", out, err)
	}
}
