package assistants

import (
	"errors"
	"os"
	"path/filepath"

	"aulaglobal-mcp/internal/atomicfile"
)

// atomicConfig replaces an assistant's configuration file with data. The
// previous content is saved as path+".bak" first. It refuses anything that is
// not a regular file, so a symlink is never replaced.
func atomicConfig(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("Configuración no regular; no se ha cambiado nada")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	previous, err := os.ReadFile(path)
	if err == nil {
		if err = atomicfile.Write(path+".bak", previous); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return atomicfile.Write(path, data)
}
