package uninstall

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aulaglobal-mcp/internal/testutil"
)

func TestWindowsCleanupCommand(t *testing.T) {
	got, ok := windowsCleanupCommand(`C:\Users\Ana\AppData\Local\Programs\aulaglobal-mcp\aulaglobal-mcp.exe.old`, `C:\Users\Ana\AppData\Local\Programs\aulaglobal-mcp`)
	want := `cmd.exe /d /s /c "ping -n 3 127.0.0.1 >nul & del /f /q "C:\Users\Ana\AppData\Local\Programs\aulaglobal-mcp\aulaglobal-mcp.exe.old" & rmdir "C:\Users\Ana\AppData\Local\Programs\aulaglobal-mcp""`
	if !ok || got != want {
		t.Fatalf("%q", got)
	}
	got, ok = windowsCleanupCommand(`C:\bin\a.exe.old`, "")
	if !ok || strings.Contains(got, "rmdir") {
		t.Fatalf("%q", got)
	}
	for _, bad := range []string{`C:\a&b\x.old`, `C:\100%\x.old`, `C:\a^b\x.old`, "C:\\a\"b\\x.old", `C:\a|b\x.old`} {
		if _, ok := windowsCleanupCommand(bad, ""); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, ok := windowsCleanupCommand(`C:\ok\x.old`, `C:\a&b`); ok {
		t.Error("unsafe rmdir accepted")
	}
}

func TestRemoveBinaryUnix(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "aulaglobal-mcp")
	exe := filepath.Join(dir, "aulaglobal-mcp")
	testutil.WriteFile(t, exe, "bin")
	st, msg := removeBinaryUnix(exe, true)
	if st != stOK || !strings.Contains(msg, "carpeta") {
		t.Fatal(st, msg)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("empty dedicated folder kept")
	}
	// A non-empty folder is never removed.
	testutil.WriteFile(t, exe, "bin")
	testutil.WriteFile(t, filepath.Join(dir, "keep.txt"), "mine")
	if st, _ = removeBinaryUnix(exe, true); st != stOK {
		t.Fatal(st)
	}
	if testutil.ReadFile(t, filepath.Join(dir, "keep.txt")) != "mine" {
		t.Fatal("foreign file removed")
	}
	// Already gone is fine; rmDir=false keeps the folder.
	if st, _ = removeBinaryUnix(exe, false); st != stOK {
		t.Fatal(st)
	}
	// A shared folder (rmDir=false) with only our binary stays.
	shared := filepath.Join(root, "bin")
	testutil.WriteFile(t, filepath.Join(shared, "aulaglobal-mcp"), "x")
	removeBinaryUnix(filepath.Join(shared, "aulaglobal-mcp"), false)
	if !isDir(shared) {
		t.Fatal("shared folder removed")
	}
}

func TestRemoveBinaryWindowsMovesAsideAndSchedules(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "aulaglobal-mcp")
	exe := filepath.Join(dir, "aulaglobal-mcp.exe")
	testutil.WriteFile(t, exe, "bin")
	testutil.WriteFile(t, exe+".old", "stale")
	var cmdline string
	st, msg := removeBinaryWindows(exe, true, func(c string) error { cmdline = c; return nil })
	if st != stOK || !strings.Contains(msg, exe+".old") {
		t.Fatal(st, msg)
	}
	if _, err := os.Stat(exe); !os.IsNotExist(err) || testutil.ReadFile(t, exe+".old") != "bin" {
		t.Fatal("not parked")
	}
	if !strings.Contains(cmdline, `del /f /q "`+exe+`.old"`) || !strings.Contains(cmdline, `rmdir "`+dir+`"`) {
		t.Fatal(cmdline)
	}
	// Scheduling failure: say exactly which file is left.
	testutil.WriteFile(t, exe, "bin")
	st, msg = removeBinaryWindows(exe, false, func(string) error { return errors.New("denied") })
	if st != stWarn || !strings.Contains(msg, exe+".old") {
		t.Fatal(st, msg)
	}
	// Rename failure (nothing to rename): exact file named.
	os.Remove(exe)
	st, msg = removeBinaryWindows(exe, false, func(string) error { t.Fatal("scheduled"); return nil })
	if st != stFail || !strings.Contains(msg, exe) {
		t.Fatal(st, msg)
	}
}

func TestPointsToProgram(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "aulaglobal-mcp")
	for _, tc := range []struct {
		cmd  string
		want bool
	}{
		{exe, true},
		{"/opt/old/aulaglobal-mcp", true},
		{`C:\Users\Ana\AppData\Local\Programs\aulaglobal-mcp\aulaglobal-mcp.exe`, true},
		{`C:\X\AULAGLOBAL-MCP.EXE`, true},
		{"aulaglobal-mcp", true},
		{"/usr/bin/node", false},
		{"/home/a/aulaglobal-mcp-fork/server", false},
		{"my-aulaglobal-mcp", false},
		{"", false},
	} {
		if got := pointsToProgram(tc.cmd, exe, false); got != tc.want {
			t.Errorf("%q = %v", tc.cmd, got)
		}
	}
}
