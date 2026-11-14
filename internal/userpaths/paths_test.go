package userpaths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	token, data, e := Defaults()
	if e != nil {
		t.Fatal(e)
	}
	config, _ := os.UserConfigDir()
	home, _ := os.UserHomeDir()
	if token != filepath.Join(config, "aulaglobal-mcp", "token") || data != filepath.Join(home, "AulaGlobal") {
		t.Fatal(token, data)
	}
	for _, goos := range []string{"windows", "darwin", "linux"} {
		got := Claude(goos, "HOME", "CONFIG")
		want := filepath.Join("CONFIG", "Claude", "claude_desktop_config.json")
		if goos == "darwin" {
			want = filepath.Join("HOME", "Library", "Application Support", "Claude", "claude_desktop_config.json")
		}
		if got != want {
			t.Fatal(goos, got)
		}
	}
}
