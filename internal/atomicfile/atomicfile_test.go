package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCreatesAndReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, content := range []string{"first", "second, longer than the first"} {
		if err := Write(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != content {
			t.Fatalf("content = %q, %v; want %q", got, err, content)
		}
	}
}

func TestWriteLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	if err := Write(filepath.Join(dir, "config.json"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("directory holds %d entries, %v", len(entries), err)
	}
}

func TestWriteFailsWithoutDirectory(t *testing.T) {
	if err := Write(filepath.Join(t.TempDir(), "missing", "config.json"), []byte("x")); err == nil {
		t.Fatal("wrote into a directory that does not exist")
	}
}
