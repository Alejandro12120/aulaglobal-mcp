package uninstall

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"aulaglobal-mcp/internal/atomicfile"
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
	for _, form := range []struct{ prefix, suffix string }{
		{`export PATH="`, `:$PATH" ` + pathMarker},
		{`set -gx PATH "`, `" $PATH ` + pathMarker},
	} {
		if strings.HasPrefix(line, form.prefix) && strings.HasSuffix(line, form.suffix) && len(line) > len(form.prefix)+len(form.suffix) {
			return line[len(form.prefix) : len(line)-len(form.suffix)], true
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
	for i, line := range lines {
		if dir, ok := markedPathLine(line); ok {
			if match(expandHome(dir, home)) {
				removed++
				// install.sh writes a blank line before its own; take it along.
				if count := len(kept); count > 0 && kept[count-1] == "" && (i+1 >= len(lines) || lines[i+1] == "") {
					kept = kept[:count-1]
				}
				continue
			}
			others = append(others, dir)
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n"), removed, others
}

// rewriteProfile saves path+".bak" with the original and replaces the file
// atomically, keeping its permission bits. A symlinked profile (dotfile
// managers) is edited at its target.
func rewriteProfile(path string, content []byte) error {
	target := path
	if info, err := os.Lstat(path); err != nil {
		return err
	} else if info.Mode()&os.ModeSymlink != 0 {
		if target, err = filepath.EvalSymlinks(path); err != nil {
			return err
		}
	}
	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("no es un archivo normal")
	}
	old, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	if err = atomicfile.Write(path+".bak", old); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".profile-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(content); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), info.Mode().Perm())
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
func expandPercent(text string, getenv func(string) string) string {
	var out strings.Builder
	for {
		i := strings.IndexByte(text, '%')
		if i < 0 {
			break
		}
		j := strings.IndexByte(text[i+1:], '%')
		if j < 0 {
			break
		}
		name := text[i+1 : i+1+j]
		if value := getenv(name); name != "" && value != "" {
			out.WriteString(text[:i] + value)
		} else {
			out.WriteString(text[:i+j+2])
		}
		text = text[i+j+2:]
	}
	out.WriteString(text)
	return out.String()
}

func normalizePathEntry(err string, getenv func(string) string) string {
	err = strings.TrimSpace(expandPercent(strings.TrimSpace(err), getenv))
	err = strings.ReplaceAll(err, "/", `\`)
	err = strings.TrimRight(err, `\`)
	return strings.ToLower(err)
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
	for _, entry := range parts {
		if strings.TrimSpace(entry) != "" && normalizePathEntry(entry, getenv) == want {
			removed = true
			continue
		}
		kept = append(kept, entry)
	}
	if !removed {
		return value, false
	}
	return strings.Join(kept, ";"), true
}
