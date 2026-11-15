package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveAside(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "aulaglobal-mcp.exe")
	if err := os.WriteFile(exe, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A leftover from an earlier update is replaced.
	if err := os.WriteFile(exe+".old", []byte("stale"), 0o755); err != nil {
		t.Fatal(err)
	}
	old, err := MoveAside(exe)
	if err != nil || old != exe+".old" {
		t.Fatal(old, err)
	}
	if b, _ := os.ReadFile(old); string(b) != "new" {
		t.Fatalf("old = %q", b)
	}
	if _, err := os.Stat(exe); !os.IsNotExist(err) {
		t.Fatal("executable still in place")
	}
	if _, err := MoveAside(exe); err == nil {
		t.Fatal("moving a missing file succeeded")
	}
}
