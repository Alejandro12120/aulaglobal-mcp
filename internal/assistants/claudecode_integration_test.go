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
	e := assistants.RealEnv(runtime.GOOS, home, filepath.Join(home, "config"))
	realRun := e.RunClaude
	e.LookPath = func(n string) (string, error) {
		if n == "claude" {
			return cli, nil
		}
		return "", os.ErrNotExist
	}
	e.RunClaude = func(ctx context.Context, path string, args ...string) ([]byte, error) {
		if os.Getenv("HOME") != home || home == "" || !strings.HasPrefix(home, os.TempDir()+string(os.PathSeparator)) {
			t.Fatal("unsafe HOME")
		}
		b, err := realRun(ctx, path, args...)
		t.Logf("claude %s: %s (error %v)", strings.Join(args, " "), strings.TrimSpace(string(b)), err)
		return b, err
	}
	call := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		b, err := e.RunClaude(ctx, cli, args...)
		if err != nil {
			t.Fatalf("%s: %s (%v)", args, b, err)
		}
		return string(b)
	}
	a := assistantstest.ByID(t, "claude-code")
	w := assistants.Entry{Exe: "/bin/true", Extra: []string{"-e", "AULAGLOBAL_DOWNLOAD_DIR=" + filepath.Join(home, "notes with spaces")}}
	if i := a.Inspect(e, w); i.Err != nil || i.State != assistants.Missing {
		t.Fatal(i)
	}
	for _, scope := range []string{"local", "project"} {
		call("mcp", "add", "--scope", scope, "aulaglobal", "--", "/bin/true")
		i := a.Inspect(e, w)
		if i.Err != nil || i.State != assistants.OtherScope || i.Scope != scope {
			t.Fatal(i)
		}
		project, _ := os.ReadFile(".mcp.json")
		if err := a.Apply(e, w, true); err != nil {
			t.Fatal(err)
		}
		if scope == "project" {
			after, _ := os.ReadFile(".mcp.json")
			if string(after) != string(project) {
				t.Fatal("project file changed")
			}
			call("mcp", "remove", "aulaglobal", "--scope", "project")
		}
		b := call("mcp", "get", "aulaglobal")
		if !strings.Contains(b, "Scope: User config (available in all your projects)") {
			t.Fatal(b)
		}
		// /bin/true has no args, whereas desired entry does, so update then inspect.
		if err := a.Apply(e, w, true); err != nil {
			t.Fatal(err)
		}
		if i := a.Inspect(e, w); i.Err != nil || i.State != assistants.Identical {
			t.Fatal(i)
		}
		call("mcp", "remove", "aulaglobal", "--scope", "user")
	}
}
