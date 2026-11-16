// Package testutil has helpers shared by the tests of several packages.
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WriteFile writes content to path, creating its directory.
func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// ReadFile returns the content of path.
func ReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// SymlinkOrSkip creates link -> target, skipping the test where the platform
// cannot create symlinks (Windows without the privilege) or does not report them
// as symlinks (Wine), since the code under test relies on Lstat telling them apart.
func SymlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Skip("this platform does not report symlinks to Lstat")
	}
}

// Golden compares got with testdata/<name>.golden in the test's package
// directory. UPDATE_GOLDEN=1 rewrites the file.
func Golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(got), 0600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := lineDifference(string(want), got); diff != "" {
		t.Fatalf("transcript mismatch: %s\n%s", diff, got)
	}
}

// lineDifference quotes the first differing line so invisible whitespace is
// visible in CI logs. Line endings are not compared.
func lineDifference(want, got string) string {
	want = strings.ReplaceAll(want, "\r\n", "\n")
	got = strings.ReplaceAll(got, "\r\n", "\n")
	if want == got {
		return ""
	}
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		if i >= len(wantLines) {
			return fmt.Sprintf("line %d: want <EOF>, got %q", i+1, gotLines[i])
		}
		if i >= len(gotLines) {
			return fmt.Sprintf("line %d: want %q, got <EOF>", i+1, wantLines[i])
		}
		if wantLines[i] != gotLines[i] {
			return fmt.Sprintf("line %d: want %q, got %q", i+1, wantLines[i], gotLines[i])
		}
	}
	return ""
}
