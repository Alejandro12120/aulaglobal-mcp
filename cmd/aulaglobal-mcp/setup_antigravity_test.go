package main

import (
	"aulaglobal-mcp/internal/userpaths"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestFindAntigravity(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		home, local := "HOME", "LOCAL"
		candidates := []struct {
			path string
			mode os.FileMode
		}{{filepath.Join(home, ".gemini", "antigravity"), os.ModeDir | 0700}, {filepath.Dir(userpaths.Antigravity(home)), os.ModeDir | 0700}}
		switch goos {
		case "linux":
			candidates = append(candidates, struct {
				path string
				mode os.FileMode
			}{"/usr/share/antigravity/antigravity", 0755})
		case "darwin":
			candidates = append(candidates, struct {
				path string
				mode os.FileMode
			}{"/Applications/Antigravity.app", os.ModeDir | 0755})
		case "windows":
			candidates = append(candidates, struct {
				path string
				mode os.FileMode
			}{filepath.Join(local, "Programs", "Antigravity", "Antigravity.exe"), 0644})
		}
		look := func(string) (string, error) { return "", os.ErrNotExist }
		for _, c := range candidates {
			stat := func(p string) (os.FileInfo, error) {
				if p != c.path {
					return nil, os.ErrNotExist
				}
				return fstest.MapFS{"x": &fstest.MapFile{Mode: c.mode}}.Stat("x")
			}
			if _, ok := findAntigravity(goos, home, local, look, stat); !ok {
				t.Fatal(goos, c)
			}
		}
		missing := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
		if _, ok := findAntigravity(goos, home, local, look, missing); ok {
			t.Fatal("present")
		}
		cli, ok := findAntigravity(goos, home, local, func(n string) (string, error) {
			if n == "agy" {
				return "fake-cli", nil
			}
			return "", os.ErrNotExist
		}, missing)
		if !ok || cli != "fake-cli" {
			t.Fatal(cli, ok)
		}
	}
}

func TestRealAntigravity(t *testing.T) {
	cli := os.Getenv("AULAGLOBAL_TEST_AGY")
	if cli == "" {
		t.Skip("set AULAGLOBAL_TEST_AGY to isolated binary")
	}
	entry, e := antigravityCLIEntry(cli, "/bin/true", []string{"-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes"})
	if e != nil || entry["command"] != "/bin/true" || entry["disabled"] != false {
		t.Fatal(entry, e)
	}
	t.Logf("real CLI generated entry: %v", entry)
}

func TestAntigravityHelpFixture(t *testing.T) {
	b, e := os.ReadFile("testdata/agy-1.5.9-mcp-add-help.txt")
	if e != nil || !strings.Contains(string(b), "<commandOrUrl>") || !strings.Contains(string(b), "--env") {
		t.Fatal("unexpected real CLI help", e)
	}
}
