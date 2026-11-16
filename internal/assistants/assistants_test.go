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
	for _, assistant := range assistants.All() {
		got, err := assistants.Select(assistant.ID())
		if err != nil || len(got) != 1 || got[0].ID() != assistant.ID() {
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
	entry, err := assistants.NewEntry("server", "token", "data", "token", "data")
	if err != nil || !filepath.IsAbs(entry.Exe) || entry.Config()["env"] != nil {
		t.Fatal(entry, err)
	}
	entry, err = assistants.NewEntry("server", "private", "notes", "token", "data")
	if err != nil || len(entry.Config()["env"].(map[string]string)) != 2 {
		t.Fatal(entry, err)
	}
}

func TestNoHostMetadataForSimulatedOS(t *testing.T) {
	// Explicit fake directories also exercise Detect without host permission bits.
	for _, assistant := range assistants.All() {
		env, _, _, _ := assistantstest.Fixture(t, assistant, false)
		env.GOOS = "windows"
		env.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
		env.Stat = func(string) (os.FileInfo, error) {
			return fstest.MapFS{"dir": &fstest.MapFile{Mode: os.ModeDir | 0600}}.Stat("dir")
		}
		if assistant.ID() != "claude-code" && !assistant.Detect(env) {
			t.Fatal(assistant.ID())
		}
	}
}
