package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"aulaglobal-mcp/internal/selfupdate"
)

// Everything `uninstall` deletes outside the assistants' own configuration is
// decided here, in small functions that can be tested without touching real
// files: shell-profile PATH lines, the Windows user PATH value, the materials
// folder and the program itself.

// ---- program names and paths -----------------------------------------------

const (
	programName = "aulaglobal-mcp"
	pathMarker  = "# aulaglobal-mcp"
	dataMarker  = ".aulaglobal-cache"
)

// baseName works for both separators whatever the host OS, so Windows paths can
// be reasoned about in tests that run elsewhere.
func baseName(p string) string {
	p = strings.TrimRight(p, `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// pointsToProgram reports whether an assistant entry's command is this
// executable or at least a binary named aulaglobal-mcp.
func pointsToProgram(command, exe string, windows bool) bool {
	if command == "" {
		return false
	}
	c := filepath.Clean(command)
	if c == filepath.Clean(exe) || (windows && strings.EqualFold(c, filepath.Clean(exe))) {
		return true
	}
	if a, e1 := os.Stat(c); e1 == nil {
		if b, e2 := os.Stat(exe); e2 == nil && os.SameFile(a, b) {
			return true
		}
	}
	name := strings.ToLower(baseName(command))
	return name == programName || name == programName+".exe"
}

// ---- shell profiles (macOS and Linux) ----------------------------------------

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

func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	ra, e1 := filepath.EvalSymlinks(a)
	rb, e2 := filepath.EvalSymlinks(b)
	return e1 == nil && e2 == nil && ra == rb
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

// ---- Windows user PATH -------------------------------------------------------

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

// ---- credentials -----------------------------------------------------------

// tokenFiles lists what setup stores for a token: the token, its private
// companion, its metadata and the temporaries an interrupted setup leaves.
func tokenFiles(token string) []string {
	cands := []string{token, token + ".private", token + ".meta.json"}
	if temps, err := filepath.Glob(filepath.Join(filepath.Dir(token), ".token-*")); err == nil {
		cands = append(cands, temps...)
	}
	var out []string
	for _, p := range cands {
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// ---- materials folder --------------------------------------------------------

type dataVerdict struct {
	exists  bool
	ok      bool   // may be deleted
	empty   bool   // delete with Remove, not RemoveAll
	custom  bool   // not the default location
	reason  string // why it is refused
	dirName string
}

var userFolderNames = map[string]bool{
	"documents": true, "documentos": true, "desktop": true, "escritorio": true, "downloads": true, "descargas": true,
	"pictures": true, "imágenes": true, "imagenes": true, "music": true, "música": true, "musica": true,
	"videos": true, "vídeos": true, "movies": true, "library": true, "appdata": true, "dropbox": true,
	"onedrive": true, "google drive": true, "icloud drive": true, "public": true, "applications": true,
}

// checkDataDir decides whether p may be deleted as the materials folder.
// protect lists paths that must not be inside it (program, config, cache).
func checkDataDir(p, home, def string, protect []string) dataVerdict {
	v := dataVerdict{dirName: p}
	refuse := func(why string) dataVerdict { v.reason = why; return v }
	if strings.TrimSpace(p) == "" || !filepath.IsAbs(p) {
		return refuse("la ruta no es absoluta")
	}
	p = filepath.Clean(p)
	v.dirName = p
	v.custom = !sameDir(p, def)
	if filepath.Dir(p) == p {
		return refuse("es la raíz del disco")
	}
	if home != "" && sameDir(p, home) {
		return refuse("es tu carpeta personal")
	}
	inside := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if home != "" && inside(p, home) {
		return refuse("contiene tu carpeta personal")
	}
	for _, q := range protect {
		if q != "" && inside(p, filepath.Clean(q)) {
			return refuse("contiene el programa o su configuración")
		}
	}
	if userFolderNames[strings.ToLower(filepath.Base(p))] {
		return refuse("parece una carpeta personal del sistema (" + filepath.Base(p) + ")")
	}
	fi, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return v
	}
	v.exists = true
	if err != nil {
		return refuse("no se puede leer")
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return refuse("es un enlace simbólico")
	}
	if !fi.IsDir() {
		return refuse("no es una carpeta")
	}
	if m, err := os.Lstat(filepath.Join(p, dataMarker)); err == nil && m.IsDir() {
		v.ok = true
		return v
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return refuse("no se puede leer")
	}
	if len(entries) == 0 {
		v.ok, v.empty = true, true
		return v
	}
	return refuse("no contiene la marca " + dataMarker + "; no parece una carpeta de apuntes creada por este programa")
}

// ---- the program itself ------------------------------------------------------

// removeBinaryUnix deletes the executable (Unix lets a running program be
// unlinked) and, if asked, the now-empty folder dedicated to it.
func removeBinaryUnix(exe string, rmDir bool) (stepStatus, string) {
	if err := os.Remove(exe); err != nil && !os.IsNotExist(err) {
		return stFail, "No se pudo borrar el programa. Borra tú este archivo: " + exe
	}
	msg := "Programa borrado: " + exe
	if rmDir {
		if err := os.Remove(filepath.Dir(exe)); err == nil {
			msg += " (y su carpeta, que quedó vacía)"
		}
	}
	return stOK, msg
}

// windowsCleanupCommand builds the command line of the detached helper that
// waits for this process to exit and then deletes the parked file (and the
// folder if rmdir is non-empty and it is then empty). ok is false when a path
// holds characters cmd.exe would interpret.
func windowsCleanupCommand(old, rmdir string) (string, bool) {
	for _, p := range []string{old, rmdir} {
		if strings.ContainsAny(p, "\"%&^|<>!\r\n") {
			return "", false
		}
	}
	cmd := `ping -n 3 127.0.0.1 >nul & del /f /q "` + old + `"`
	if rmdir != "" {
		cmd += ` & rmdir "` + rmdir + `"`
	}
	return `cmd.exe /d /s /c "` + cmd + `"`, true
}

// removeBinaryWindows parks the running executable as <exe>.old (a running exe
// cannot be deleted, only renamed) and schedules its deletion after exit.
func removeBinaryWindows(exe string, rmDir bool, spawn func(cmdline string) error) (stepStatus, string) {
	old, err := selfupdate.MoveAside(exe)
	if err != nil {
		return stFail, "No se pudo apartar el programa (" + err.Error() + "). Borra tú este archivo: " + exe
	}
	dir := ""
	if rmDir {
		dir = filepath.Dir(exe)
	}
	cmdline, ok := windowsCleanupCommand(old, dir)
	if ok && spawn != nil {
		if err = spawn(cmdline); err == nil {
			return stOK, "Programa retirado; " + old + " se borrará en unos segundos"
		}
	}
	return stWarn, "El programa ya no está disponible, pero queda el archivo " + old + ". Bórralo tú cuando puedas."
}
