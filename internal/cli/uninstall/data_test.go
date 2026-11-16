package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenFiles(t *testing.T) {
	dir := t.TempDir()
	tok := filepath.Join(dir, "token")
	for _, n := range []string{"token", "token.private", "token.meta.json", ".token-123", "token.notes", "other"} {
		writeFile(t, filepath.Join(dir, n), "x")
	}
	got := tokenFiles(tok)
	if len(got) != 4 {
		t.Fatalf("%v", got)
	}
	for _, g := range got {
		if strings.HasSuffix(g, "other") || strings.HasSuffix(g, "notes") {
			t.Fatal(g)
		}
	}
	if len(tokenFiles(filepath.Join(dir, "missing", "token"))) != 0 {
		t.Fatal("phantom files")
	}
}

func TestCheckDataDir(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	def := filepath.Join(home, "AulaGlobal")
	prog := filepath.Join(root, "prog")
	protect := []string{prog}
	mk := func(p string, marker bool) string {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
		if marker {
			if err := os.Mkdir(filepath.Join(p, dataMarker), 0700); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	mk(home, false)
	mk(prog, false)
	check := func(p string) dataVerdict { return checkDataDir(p, home, def, protect) }

	if v := check(mk(def, true)); !v.ok || v.empty || v.custom {
		t.Fatalf("default with marker: %+v", v)
	}
	if v := check(filepath.Join(root, "nothing")); v.ok || v.exists || v.reason != "" {
		t.Fatalf("missing: %+v", v)
	}
	if v := check(mk(filepath.Join(root, "empty"), false)); !v.ok || !v.empty || !v.custom {
		t.Fatalf("empty: %+v", v)
	}
	notes := mk(filepath.Join(root, "notes"), false)
	writeFile(t, filepath.Join(notes, "important.docx"), "x")
	for name, p := range map[string]string{
		"no marker":      notes,
		"home":           home,
		"relative":       "AulaGlobal",
		"empty string":   "",
		"root":           filepath.VolumeName(root) + string(filepath.Separator),
		"home ancestor":  root,
		"program":        prog,
		"user folder":    mk(filepath.Join(root, "Documents"), true),
		"user folder es": mk(filepath.Join(root, "Escritorio"), true),
	} {
		if v := check(p); v.ok || v.reason == "" {
			t.Errorf("%s accepted: %+v", name, v)
		}
	}
	// A file, and (where supported) a symlink to a marked folder, are refused.
	f := filepath.Join(root, "file")
	writeFile(t, f, "x")
	if v := check(f); v.ok {
		t.Errorf("file accepted")
	}
	target := mk(filepath.Join(root, "target"), true)
	link := filepath.Join(root, "link")
	t.Run("symlink", func(t *testing.T) {
		symlinkOrSkip(t, target, link)
		if v := check(link); v.ok || !strings.Contains(v.reason, "enlace") {
			t.Errorf("symlink accepted: %+v", v)
		}
	})
	// The marker must be a directory: a plain file with that name proves nothing.
	fake := mk(filepath.Join(root, "fake"), false)
	writeFile(t, filepath.Join(fake, dataMarker), "x")
	if v := check(fake); v.ok {
		t.Errorf("file marker accepted")
	}
}
