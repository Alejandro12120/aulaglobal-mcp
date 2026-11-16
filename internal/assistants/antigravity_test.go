package assistants_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/userpaths"
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
		for _, candidate := range candidates {
			stat := func(p string) (os.FileInfo, error) {
				if p != candidate.path {
					return nil, os.ErrNotExist
				}
				return fstest.MapFS{"x": &fstest.MapFile{Mode: candidate.mode}}.Stat("x")
			}
			if _, ok := assistants.FindAntigravity(goos, home, local, look, stat); !ok {
				t.Fatal(goos, candidate)
			}
		}
		missing := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
		if _, ok := assistants.FindAntigravity(goos, home, local, look, missing); ok {
			t.Fatal("present")
		}
		cli, ok := assistants.FindAntigravity(goos, home, local, func(n string) (string, error) {
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
	entry, err := assistants.AntigravityCLIEntry(cli, "/bin/true", []string{"-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes"})
	if err != nil || entry["command"] != "/bin/true" || entry["disabled"] != false {
		t.Fatal(entry, err)
	}
	t.Logf("real CLI generated entry: %v", entry)
}

func TestAntigravityHelpFixture(t *testing.T) {
	help, err := os.ReadFile("testdata/agy-1.5.9-mcp-add-help.txt")
	if err != nil || !strings.Contains(string(help), "<commandOrUrl>") || !strings.Contains(string(help), "--env") {
		t.Fatal("unexpected real CLI help", err)
	}
}
