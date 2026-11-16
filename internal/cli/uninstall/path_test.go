package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMarkedPathLineIsExactlyWhatInstallShWrites(t *testing.T) {
	for _, tc := range []struct {
		line, dir string
		ok        bool
	}{
		{`export PATH="$HOME/.local/bin:$PATH" # aulaglobal-mcp`, "$HOME/.local/bin", true},
		{`export PATH="/opt/x y/bin:$PATH" # aulaglobal-mcp`, "/opt/x y/bin", true},
		{`set -gx PATH "$HOME/.local/bin" $PATH # aulaglobal-mcp`, "$HOME/.local/bin", true},
		{`export PATH="$HOME/.local/bin:$PATH"`, "", false},                         // not marked
		{`export PATH="$HOME/.local/bin:$PATH:/other" # aulaglobal-mcp`, "", false}, // different shape
		{`export PATH="$HOME/.local/bin:$PATH" # aulaglobal-mcp # mine`, "", false},
		{`# aulaglobal-mcp`, "", false},
		{`export PATH=":$PATH" # aulaglobal-mcp`, "", false},
		{`  export PATH="/a:$PATH" # aulaglobal-mcp`, "", false},
	} {
		dir, ok := markedPathLine(tc.line)
		if ok != tc.ok || dir != tc.dir {
			t.Errorf("%q -> %q %v", tc.line, dir, ok)
		}
	}
}

func TestRemoveMarkedPathLines(t *testing.T) {
	home := "/home/ana"
	match := func(d string) bool { return filepath.Clean(d) == filepath.Clean("/home/ana/.local/bin") } // expandHome joins with the OS separator
	// install.sh appends "\n<line>\n" to whatever was there.
	in := "alias ll='ls -l'\n\nexport PATH=\"$HOME/.local/bin:$PATH\" # aulaglobal-mcp\n"
	out, n, others := removeMarkedPathLines(in, home, match)
	if out != "alias ll='ls -l'\n" || n != 1 || len(others) != 0 {
		t.Fatalf("%q %d %v", out, n, others)
	}
	if out2, n2, _ := removeMarkedPathLines(out, home, match); out2 != out || n2 != 0 {
		t.Fatal("not idempotent")
	}
	// Middle of the file, unrelated lines and a line for another directory stay.
	in = "a\n\nexport PATH=\"$HOME/.local/bin:$PATH\" # aulaglobal-mcp\n\nexport PATH=\"/opt/other:$PATH\" # aulaglobal-mcp\nexport PATH=\"$HOME/.local/bin:$PATH\"\nb"
	out, n, others = removeMarkedPathLines(in, home, match)
	want := "a\n\nexport PATH=\"/opt/other:$PATH\" # aulaglobal-mcp\nexport PATH=\"$HOME/.local/bin:$PATH\"\nb"
	if out != want || n != 1 || len(others) != 1 || others[0] != "/opt/other" {
		t.Fatalf("%q %d %v", out, n, others)
	}
}

func TestRewriteProfileKeepsModeAndBacksUp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	f := filepath.Join(t.TempDir(), ".zshrc")
	writeFile(t, f, "old\n")
	if err := os.Chmod(f, 0644); err != nil {
		t.Fatal(err)
	}
	if err := rewriteProfile(f, []byte("new\n")); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(f)
	if readFile(t, f) != "new\n" || readFile(t, f+".bak") != "old\n" || fi.Mode().Perm() != 0644 {
		t.Fatal("rewrite", fi.Mode())
	}
	// A symlinked profile is edited at its target and stays a symlink.
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "zshrc")
	link := filepath.Join(dir, ".zshrc")
	writeFile(t, target, "old\n")
	symlinkOrSkip(t, target, link)
	if err := rewriteProfile(link, []byte("new\n")); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 || readFile(t, target) != "new\n" || readFile(t, link+".bak") != "old\n" {
		t.Fatal("symlink was replaced")
	}
}

func TestProfileCandidates(t *testing.T) {
	got := profileCandidates("/h", "", "")
	want := []string{"/h/.zshrc", "/h/.bashrc", "/h/.bash_profile", "/h/.config/fish/config.fish", "/h/.profile"}
	for i := range want {
		if got[i] != filepath.FromSlash(want[i]) {
			t.Fatalf("%v", got)
		}
	}
	got = profileCandidates("/h", "/z", "/x")
	if got[0] != filepath.FromSlash("/z/.zshrc") || got[3] != filepath.FromSlash("/x/fish/config.fish") {
		t.Fatalf("%v", got)
	}
}

func TestRemovePathEntry(t *testing.T) {
	getenv := func(k string) string {
		return map[string]string{"LOCALAPPDATA": `C:\Users\Ana\AppData\Local`}[k]
	}
	dir := `C:\Users\Ana\AppData\Local\Programs\aulaglobal-mcp`
	for _, tc := range []struct {
		in, want string
		found    bool
	}{
		{`C:\Windows;` + dir + `;C:\Tools`, `C:\Windows;C:\Tools`, true},
		{dir, ``, true},
		{`C:\Windows;` + dir, `C:\Windows`, true},
		{dir + `\;C:\Tools`, `C:\Tools`, true},
		{`%LOCALAPPDATA%\Programs\AulaGlobal-MCP;C:\Tools`, `C:\Tools`, true},
		{`C:\A;;C:\B;`, `C:\A;;C:\B;`, false}, // empty segments are preserved
		{`C:\Users\Ana\AppData\Local\Programs\aulaglobal-mcp-other;C:\Tools`, `C:\Users\Ana\AppData\Local\Programs\aulaglobal-mcp-other;C:\Tools`, false},
		{`C:\Users\Ana\AppData\Local\Programs;` + dir + `\bin`, `C:\Users\Ana\AppData\Local\Programs;` + dir + `\bin`, false},
		{``, ``, false},
	} {
		got, found := removePathEntry(tc.in, dir, getenv)
		if got != tc.want || found != tc.found {
			t.Errorf("%q -> %q %v", tc.in, got, found)
		}
	}
	if _, found := removePathEntry("a;b", "", getenv); found {
		t.Error("empty dir matched")
	}
	if got := expandPercent(`%A%\x\%UNKNOWN%\%`, func(k string) string { return map[string]string{"A": "val"}[k] }); got != `val\x\%UNKNOWN%\%` {
		t.Errorf("%q", got)
	}
}
