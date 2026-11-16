package assistants_test

import (
	"os"
	"path/filepath"
	"strings"
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

func TestIDsListEveryAssistantInSetupOrder(t *testing.T) {
	want := "claude-code,claude-desktop,codex,opencode,antigravity"
	if got := assistants.IDs(); got != want {
		t.Errorf("IDs() = %q, want %q", got, want)
	}
	all := assistants.All()
	if len(all) != 5 {
		t.Fatalf("%d assistants registered, want 5", len(all))
	}
	for i, id := range strings.Split(want, ",") {
		if all[i].ID() != id {
			t.Errorf("assistant %d is %q, want %q", i, all[i].ID(), id)
		}
	}
}

// Anything in Extra other than the two variables this program defines must
// never reach an assistant's configuration.
func TestEntryConfigDropsUnapprovedVariables(t *testing.T) {
	entry := assistants.Entry{Exe: "/server", Extra: []string{"-e", "SECRET=never", "-e", "AULAGLOBAL_TOKEN_FILE=/token"}}
	env, _ := entry.Config()["env"].(map[string]string)
	if len(env) != 1 || env["AULAGLOBAL_TOKEN_FILE"] != "/token" {
		t.Errorf("env = %v, want only AULAGLOBAL_TOKEN_FILE", env)
	}
	if _, found := env["SECRET"]; found {
		t.Error("an unapproved variable was kept")
	}
}
