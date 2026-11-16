package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Shell profile and Windows PATH edits that `uninstall` makes. Each function
// works on strings, so tests can run it without touching a real profile or the
// registry.

// pathMarker ends the PATH line that install.sh adds to a shell profile.
const pathMarker = "# aulaglobal-mcp"

// profileCandidates lists every file install.sh may have written the PATH line
// to, whatever the current shell: zsh, bash (also macOS .bash_profile), fish
// and the generic .profile.
func profileCandidates(home, zdotdir, xdg string) []string {
	if zdotdir == "" {
		zdotdir = home
	}
	if xdg == "" {
		xdg = filepath.Join(home, ".config")
	}
	return []string{
		filepath.Join(zdotdir, ".zshrc"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(xdg, "fish", "config.fish"),
		filepath.Join(home, ".profile"),
	}
}

// markedPathLine recognises exactly the two lines install.sh writes and returns
// the directory they add to PATH.
func markedPathLine(line string) (string, bool) {
	for _, f := range []struct{ prefix, suffix string }{
		{`export PATH="`, `:$PATH" ` + pathMarker},
		{`set -gx PATH "`, `" $PATH ` + pathMarker},
	} {
		if strings.HasPrefix(line, f.prefix) && strings.HasSuffix(line, f.suffix) && len(line) > len(f.prefix)+len(f.suffix) {
			return line[len(f.prefix) : len(line)-len(f.suffix)], true
		}
	}
	return "", false
}

func expandHome(dir, home string) string {
	if dir == "$HOME" {
		return home
	}
	if strings.HasPrefix(dir, "$HOME/") {
		return filepath.Join(home, dir[len("$HOME/"):])
	}
	return dir
}

// removeMarkedPathLines drops the marked lines whose directory satisfies match.
// Every other byte is kept. Lines marked for another directory are returned so
// the caller can say they were left alone.
func removeMarkedPathLines(content, home string, match func(dir string) bool) (out string, removed int, others []string) {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for i, l := range lines {
		if dir, ok := markedPathLine(l); ok {
			if match(expandHome(dir, home)) {
				removed++
				// install.sh writes a blank line before its own; take it along.
				if n := len(kept); n > 0 && kept[n-1] == "" && (i+1 >= len(lines) || lines[i+1] == "") {
					kept = kept[:n-1]
				}
				continue
			}
			others = append(others, dir)
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n"), removed, others
}

// rewriteProfile saves path+".bak" with the original and replaces the file
// atomically, keeping its permission bits. A symlinked profile (dotfile
// managers) is edited at its target.
func rewriteProfile(path string, b []byte) error {
	target := path
	if fi, err := os.Lstat(path); err != nil {
		return err
	} else if fi.Mode()&os.ModeSymlink != 0 {
		if target, err = filepath.EvalSymlinks(path); err != nil {
			return err
		}
	}
	fi, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("no es un archivo normal")
	}
	old, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	if err = writeAtomic(path+".bak", old); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".profile-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(b); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), fi.Mode().Perm())
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), target)
}

// pathStore is the user's PATH variable (HKCU\Environment\Path on Windows);
// injected so tests never see the registry.
type pathStore interface {
	Get() (string, error)
	Set(string) error
}

// expandPercent expands %NAME% references the way Windows does; unknown names
// are left as typed.
func expandPercent(s string, getenv func(string) string) string {
	var out strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			break
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			break
		}
		name := s[i+1 : i+1+j]
		if v := getenv(name); name != "" && v != "" {
			out.WriteString(s[:i] + v)
		} else {
			out.WriteString(s[:i+j+2])
		}
		s = s[i+j+2:]
	}
	out.WriteString(s)
	return out.String()
}

func normalizePathEntry(e string, getenv func(string) string) string {
	e = strings.TrimSpace(expandPercent(strings.TrimSpace(e), getenv))
	e = strings.ReplaceAll(e, "/", `\`)
	e = strings.TrimRight(e, `\`)
	return strings.ToLower(e)
}

// removePathEntry removes the entries of value equal to dir (same comparison as
// install.ps1: expanded, trimmed, no trailing slash, case-insensitive). Other
// entries, including empty ones, are kept exactly.
func removePathEntry(value, dir string, getenv func(string) string) (string, bool) {
	want := normalizePathEntry(dir, getenv)
	if want == "" {
		return value, false
	}
	parts := strings.Split(value, ";")
	kept := parts[:0:0]
	removed := false
	for _, p := range parts {
		if strings.TrimSpace(p) != "" && normalizePathEntry(p, getenv) == want {
			removed = true
			continue
		}
		kept = append(kept, p)
	}
	if !removed {
		return value, false
	}
	return strings.Join(kept, ";"), true
}
