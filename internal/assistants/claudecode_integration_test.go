package assistants_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/assistants/assistantstest"
)

// Compiled with Docker, executed on the host so Claude is available. Every
// subprocess inherits the isolated HOME and cwd, including local removal.
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
	env := assistants.RealEnv(runtime.GOOS, home, filepath.Join(home, "config"))
	realRun := env.RunClaude
	env.LookPath = func(n string) (string, error) {
		if n == "claude" {
			return cli, nil
		}
		return "", os.ErrNotExist
	}
	env.RunClaude = func(ctx context.Context, path string, args ...string) ([]byte, error) {
		if os.Getenv("HOME") != home || home == "" || !strings.HasPrefix(home, os.TempDir()+string(os.PathSeparator)) {
			t.Fatal("unsafe HOME")
		}
		output, err := realRun(ctx, path, args...)
		t.Logf("claude %s: %s (error %v)", strings.Join(args, " "), strings.TrimSpace(string(output)), err)
		return output, err
	}
	call := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		output, err := env.RunClaude(ctx, cli, args...)
		if err != nil {
			t.Fatalf("%s: %s (%v)", args, output, err)
		}
		return string(output)
	}
	assistant := assistantstest.ByID(t, "claude-code")
	entry := assistants.Entry{Exe: "/bin/true", Extra: []string{"-e", "AULAGLOBAL_DOWNLOAD_DIR=" + filepath.Join(home, "notes with spaces")}}
	if inspection := assistant.Inspect(env, entry); inspection.Err != nil || inspection.State != assistants.Missing {
		t.Fatal(inspection)
	}
	for _, scope := range []string{"local", "project"} {
		call("mcp", "add", "--scope", scope, "aulaglobal", "--", "/bin/true")
		inspection := assistant.Inspect(env, entry)
		if inspection.Err != nil || inspection.State != assistants.OtherScope || inspection.Scope != scope {
			t.Fatal(inspection)
		}
		project, _ := os.ReadFile(".mcp.json")
		if err := assistant.Apply(env, entry, true); err != nil {
			t.Fatal(err)
		}
		if scope == "project" {
			after, _ := os.ReadFile(".mcp.json")
			if string(after) != string(project) {
				t.Fatal("project file changed")
			}
			call("mcp", "remove", "aulaglobal", "--scope", "project")
		}
		output := call("mcp", "get", "aulaglobal")
		if !strings.Contains(output, "Scope: User config (available in all your projects)") {
			t.Fatal(output)
		}
		// /bin/true has no args, whereas desired entry does, so update then inspect.
		if err := assistant.Apply(env, entry, true); err != nil {
			t.Fatal(err)
		}
		if inspection := assistant.Inspect(env, entry); inspection.Err != nil || inspection.State != assistants.Identical {
			t.Fatal(inspection)
		}
		call("mcp", "remove", "aulaglobal", "--scope", "user")
	}
}
