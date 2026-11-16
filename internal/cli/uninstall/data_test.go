package uninstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aulaglobal-mcp/internal/testutil"
)

func TestTokenFiles(t *testing.T) {
	dir := t.TempDir()
	tok := filepath.Join(dir, "token")
	for _, n := range []string{"token", "token.private", "token.meta.json", ".token-123", "token.notes", "other"} {
		testutil.WriteFile(t, filepath.Join(dir, n), "x")
	}
	got := tokenFiles(tok)
	if len(got) != 4 {
		t.Fatalf("%v", got)
	}
	for _, path := range got {
		if strings.HasSuffix(path, "other") || strings.HasSuffix(path, "notes") {
			t.Fatal(path)
		}
	}
	if len(tokenFiles(filepath.Join(dir, "missing", "token"))) != 0 {
		t.Fatal("phantom files")
	}
}

func TestCheckDataDir(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	defaultDir := filepath.Join(home, "AulaGlobal")
	prog := filepath.Join(root, "prog")
	protect := []string{prog}
	makeDir := func(path string, marker bool) string {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if marker {
			if err := os.Mkdir(filepath.Join(path, dataMarker), 0700); err != nil {
				t.Fatal(err)
			}
		}
		return path
	}
	makeDir(home, false)
	makeDir(prog, false)
	check := func(path string) dataVerdict { return checkDataDir(path, home, defaultDir, protect) }

	if verdict := check(makeDir(defaultDir, true)); !verdict.ok || verdict.empty || verdict.custom {
		t.Fatalf("default with marker: %+v", verdict)
	}
	if verdict := check(filepath.Join(root, "nothing")); verdict.ok || verdict.exists || verdict.reason != "" {
		t.Fatalf("missing: %+v", verdict)
	}
	if verdict := check(makeDir(filepath.Join(root, "empty"), false)); !verdict.ok || !verdict.empty || !verdict.custom {
		t.Fatalf("empty: %+v", verdict)
	}
	notes := makeDir(filepath.Join(root, "notes"), false)
	testutil.WriteFile(t, filepath.Join(notes, "important.docx"), "x")
	for name, path := range map[string]string{
		"no marker":      notes,
		"home":           home,
		"relative":       "AulaGlobal",
		"empty string":   "",
		"root":           filepath.VolumeName(root) + string(filepath.Separator),
		"home ancestor":  root,
		"program":        prog,
		"user folder":    makeDir(filepath.Join(root, "Documents"), true),
		"user folder es": makeDir(filepath.Join(root, "Escritorio"), true),
	} {
		if verdict := check(path); verdict.ok || verdict.reason == "" {
			t.Errorf("%s accepted: %+v", name, verdict)
		}
	}
	// A file, and (where supported) a symlink to a marked folder, are refused.
	f := filepath.Join(root, "file")
	testutil.WriteFile(t, f, "x")
	if verdict := check(f); verdict.ok {
		t.Errorf("file accepted")
	}
	target := makeDir(filepath.Join(root, "target"), true)
	link := filepath.Join(root, "link")
	t.Run("symlink", func(t *testing.T) {
		testutil.SymlinkOrSkip(t, target, link)
		if verdict := check(link); verdict.ok || !strings.Contains(verdict.reason, "enlace") {
			t.Errorf("symlink accepted: %+v", verdict)
		}
	})
	// The marker must be a directory: a plain file with that name proves nothing.
	fake := makeDir(filepath.Join(root, "fake"), false)
	testutil.WriteFile(t, filepath.Join(fake, dataMarker), "x")
	if verdict := check(fake); verdict.ok {
		t.Errorf("file marker accepted")
	}
}
