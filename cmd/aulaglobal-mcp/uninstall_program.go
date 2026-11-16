package main

import (
	"os"
	"path/filepath"
	"strings"

	"aulaglobal-mcp/internal/selfupdate"
)

const programName = "aulaglobal-mcp"

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
