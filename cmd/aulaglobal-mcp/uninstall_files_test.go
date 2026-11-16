package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestWindowsCleanupCommand(t *testing.T) {
	got, ok := windowsCleanupCommand(`C:\Users\Ana\AppData\Local\Programs\aulaglobal-mcp\aulaglobal-mcp.exe.old`, `C:\Users\Ana\AppData\Local\Programs\aulaglobal-mcp`)
	want := `cmd.exe /d /s /c "ping -n 3 127.0.0.1 >nul & del /f /q "C:\Users\Ana\AppData\Local\Programs\aulaglobal-mcp\aulaglobal-mcp.exe.old" & rmdir "C:\Users\Ana\AppData\Local\Programs\aulaglobal-mcp""`
	if !ok || got != want {
		t.Fatalf("%q", got)
	}
	got, ok = windowsCleanupCommand(`C:\bin\a.exe.old`, "")
	if !ok || strings.Contains(got, "rmdir") {
		t.Fatalf("%q", got)
	}
	for _, bad := range []string{`C:\a&b\x.old`, `C:\100%\x.old`, `C:\a^b\x.old`, "C:\\a\"b\\x.old", `C:\a|b\x.old`} {
		if _, ok := windowsCleanupCommand(bad, ""); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, ok := windowsCleanupCommand(`C:\ok\x.old`, `C:\a&b`); ok {
		t.Error("unsafe rmdir accepted")
	}
}

func TestRemoveBinaryUnix(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "aulaglobal-mcp")
	exe := filepath.Join(dir, "aulaglobal-mcp")
	writeFile(t, exe, "bin")
	st, msg := removeBinaryUnix(exe, true)
	if st != stOK || !strings.Contains(msg, "carpeta") {
		t.Fatal(st, msg)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("empty dedicated folder kept")
	}
	// A non-empty folder is never removed.
	writeFile(t, exe, "bin")
	writeFile(t, filepath.Join(dir, "keep.txt"), "mine")
	if st, _ = removeBinaryUnix(exe, true); st != stOK {
		t.Fatal(st)
	}
	if readFile(t, filepath.Join(dir, "keep.txt")) != "mine" {
		t.Fatal("foreign file removed")
	}
	// Already gone is fine; rmDir=false keeps the folder.
	if st, _ = removeBinaryUnix(exe, false); st != stOK {
		t.Fatal(st)
	}
	// A shared folder (rmDir=false) with only our binary stays.
	shared := filepath.Join(root, "bin")
	writeFile(t, filepath.Join(shared, "aulaglobal-mcp"), "x")
	removeBinaryUnix(filepath.Join(shared, "aulaglobal-mcp"), false)
	if !isDir(shared) {
		t.Fatal("shared folder removed")
	}
}

func TestRemoveBinaryWindowsMovesAsideAndSchedules(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "aulaglobal-mcp")
	exe := filepath.Join(dir, "aulaglobal-mcp.exe")
	writeFile(t, exe, "bin")
	writeFile(t, exe+".old", "stale")
	var cmdline string
	st, msg := removeBinaryWindows(exe, true, func(c string) error { cmdline = c; return nil })
	if st != stOK || !strings.Contains(msg, exe+".old") {
		t.Fatal(st, msg)
	}
	if _, err := os.Stat(exe); !os.IsNotExist(err) || readFile(t, exe+".old") != "bin" {
		t.Fatal("not parked")
	}
	if !strings.Contains(cmdline, `del /f /q "`+exe+`.old"`) || !strings.Contains(cmdline, `rmdir "`+dir+`"`) {
		t.Fatal(cmdline)
	}
	// Scheduling failure: say exactly which file is left.
	writeFile(t, exe, "bin")
	st, msg = removeBinaryWindows(exe, false, func(string) error { return errors.New("denied") })
	if st != stWarn || !strings.Contains(msg, exe+".old") {
		t.Fatal(st, msg)
	}
	// Rename failure (nothing to rename): exact file named.
	os.Remove(exe)
	st, msg = removeBinaryWindows(exe, false, func(string) error { t.Fatal("scheduled"); return nil })
	if st != stFail || !strings.Contains(msg, exe) {
		t.Fatal(st, msg)
	}
}
func TestPointsToProgram(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "aulaglobal-mcp")
	for _, tc := range []struct {
		cmd  string
		want bool
	}{
		{exe, true},
		{"/opt/old/aulaglobal-mcp", true},
		{`C:\Users\Ana\AppData\Local\Programs\aulaglobal-mcp\aulaglobal-mcp.exe`, true},
		{`C:\X\AULAGLOBAL-MCP.EXE`, true},
		{"aulaglobal-mcp", true},
		{"/usr/bin/node", false},
		{"/home/a/aulaglobal-mcp-fork/server", false},
		{"my-aulaglobal-mcp", false},
		{"", false},
	} {
		if got := pointsToProgram(tc.cmd, exe, false); got != tc.want {
			t.Errorf("%q = %v", tc.cmd, got)
		}
	}
}
