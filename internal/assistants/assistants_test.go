package assistants_test

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/assistants/assistantstest"
)

func TestSelect(t *testing.T) {
	for _, a := range assistants.All() {
		got, err := assistants.Select(a.ID())
		if err != nil || len(got) != 1 || got[0].ID() != a.ID() {
			t.Fatal(got, err)
		}
	}
	if _, err := assistants.Select("bad"); err == nil {
		t.Fatal("invalid ID")
	}
	got, err := assistants.Select("codex,claude-code,codex")
	if err != nil || len(got) != 2 || got[0].ID() != "claude-code" {
		t.Fatal(got, err)
	}
}

func TestNewEntryDefaults(t *testing.T) {
	w, err := assistants.NewEntry("server", "token", "data", "token", "data")
	if err != nil || !filepath.IsAbs(w.Exe) || w.Config()["env"] != nil {
		t.Fatal(w, err)
	}
	w, err = assistants.NewEntry("server", "private", "notes", "token", "data")
	if err != nil || len(w.Config()["env"].(map[string]string)) != 2 {
		t.Fatal(w, err)
	}
}

func TestNoHostMetadataForSimulatedOS(t *testing.T) {
	// Explicit fake directories also exercise Detect without host permission bits.
	for _, a := range assistants.All() {
		e, _, _, _ := assistantstest.Fixture(t, a, false)
		e.GOOS = "windows"
		e.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
		e.Stat = func(string) (os.FileInfo, error) {
			return fstest.MapFS{"dir": &fstest.MapFile{Mode: os.ModeDir | 0600}}.Stat("dir")
		}
		if a.ID() != "claude-code" && !a.Detect(e) {
			t.Fatal(a.ID())
		}
	}
}
