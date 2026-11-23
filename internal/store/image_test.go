package store

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestImage(t *testing.T) {
	store, err := Open(t.TempDir(), 1<<20, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Root.Close()
	var pngBytes bytes.Buffer
	if err := png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) {
		t.Helper()
		if err := store.Root.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := store.Root.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("Curso/ok.png", pngBytes.Bytes())
	write("Curso/disguised.bin", pngBytes.Bytes()) // the extension is not trusted
	write("Curso/text.png", []byte("just text, not an image"))
	write("Curso/page.png", []byte("<html><script>alert(1)</script></html>"))
	write("Curso/vector.png", []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	write("Curso/big.png", append(append([]byte(nil), pngBytes.Bytes()...), make([]byte, MaxImageBytes)...))
	write("Curso/limit.png", append(append([]byte(nil), pngBytes.Bytes()...), make([]byte, MaxImageBytes-pngBytes.Len())...))
	write(".aulaglobal-cache/secret.png", pngBytes.Bytes())

	for _, name := range []string{"Curso/ok.png", "Curso/disguised.bin", "Curso/limit.png"} {
		data, mime, err := store.Image(context.Background(), name)
		if err != nil || mime != "image/png" || len(data) == 0 {
			t.Errorf("%s: %q %v", name, mime, err)
		}
	}
	data, _, _ := store.Image(context.Background(), "Curso/ok.png")
	if !bytes.Equal(data, pngBytes.Bytes()) {
		t.Error("bytes changed")
	}
	for name, want := range map[string]string{
		"Curso/text.png":               "not a supported image",
		"Curso/page.png":               "not a supported image",
		"Curso/vector.png":             "not a supported image",
		"Curso/big.png":                "too large",
		"Curso":                        "regular file",
		"../outside.png":               "relative to download root",
		"/etc/passwd":                  "relative to download root",
		"Curso/../../x.png":            "relative to download root",
		".aulaglobal-cache/secret.png": "relative to download root",
		"Curso/missing.png":            "",
	} {
		if _, _, err := store.Image(context.Background(), name); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want error containing %q", name, err, want)
		}
	}
}

func TestImageRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	store, err := Open(dir, 1<<20, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Root.Close()
	var pngBytes bytes.Buffer
	png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	if err := os.WriteFile(filepath.Join(dir, "real.png"), pngBytes.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.png", filepath.Join(dir, "link.png")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Image(context.Background(), "link.png"); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Errorf("symlink: %v", err)
	}
}
