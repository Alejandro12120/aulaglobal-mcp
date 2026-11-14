package selfupdate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// ResolveExecutable returns the real path of the running binary.
func ResolveExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// oldPath is where Windows parks the previous executable.
func oldPath(exe string) string { return exe + ".old" }

// CleanupOld removes the ".old" file a previous Windows update left next to the
// executable. Best effort: the file may still be locked by a running process.
func CleanupOld() {
	if runtime.GOOS != "windows" {
		return
	}
	if exe, err := ResolveExecutable(); err == nil {
		removeOld(exe)
	}
}

func removeOld(exe string) { _ = os.Remove(oldPath(exe)) }

// installBinary atomically replaces exe with data. The new file is written
// next to exe so the final rename never crosses a filesystem. On failure the
// original executable is left in place.
func installBinary(exe string, data []byte, windows bool) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".aulaglobal-mcp-update-*")
	if err != nil {
		return fmt.Errorf("no se puede escribir en %s (%w); descarga la versión nueva manualmente desde la página de versiones y sustituye el programa", dir, unwrapPath(err))
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmpName, 0o755)
	}
	if err != nil {
		return fmt.Errorf("no se pudo preparar el ejecutable nuevo: %w", unwrapPath(err))
	}
	if !windows {
		if err = os.Rename(tmpName, exe); err != nil {
			return fmt.Errorf("no se pudo sustituir %s: %w", exe, unwrapPath(err))
		}
		committed = true
		return nil
	}
	// Windows cannot overwrite a running executable but can rename it.
	old := oldPath(exe)
	if err = os.Remove(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no se pudo retirar %s de una actualización anterior: %w", old, unwrapPath(err))
	}
	if err = os.Rename(exe, old); err != nil {
		return fmt.Errorf("no se pudo apartar el programa actual: %w", unwrapPath(err))
	}
	if err = os.Rename(tmpName, exe); err != nil {
		if rerr := os.Rename(old, exe); rerr != nil {
			return fmt.Errorf("no se pudo instalar la versión nueva (%w) ni restaurar la anterior, que queda en %s: %v", unwrapPath(err), old, unwrapPath(rerr))
		}
		return fmt.Errorf("no se pudo instalar la versión nueva (se mantiene la actual): %w", unwrapPath(err))
	}
	committed = true
	return nil
}

// unwrapPath drops the redundant path prefix of *PathError for readability.
func unwrapPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
