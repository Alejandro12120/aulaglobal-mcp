package main

import (
	"errors"
	"os"
	"path/filepath"
)

// atomicConfig replaces an assistant's configuration file with b. The previous
// content is saved as path+".bak" first. It refuses anything that is not a
// regular file, so a symlink is never replaced.
func atomicConfig(path string, b []byte) error {
	if f, e := os.Lstat(path); e == nil && !f.Mode().IsRegular() {
		return errors.New("Configuración no regular; no se ha cambiado nada")
	} else if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	old, e := os.ReadFile(path)
	if e == nil {
		if e = writeAtomic(path+".bak", old); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	return writeAtomic(path, b)
}

// writeAtomic writes through a temporary file in the same directory and renames
// it over path, so a reader never sees a half-written file.
func writeAtomic(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".config-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
