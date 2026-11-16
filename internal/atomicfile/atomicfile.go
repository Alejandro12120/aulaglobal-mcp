// Package atomicfile writes files so that a reader never sees a half-written one.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write writes data to a temporary file in the same directory and renames it
// over path.
func Write(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
