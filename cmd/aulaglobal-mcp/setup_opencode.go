package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func findOpenCode(goos, home, dir string, look func(string) (string, error), stat func(string) (os.FileInfo, error)) bool {
	if f, e := stat(dir); e == nil && f.IsDir() {
		return true
	}
	if _, e := look("opencode"); e == nil {
		return true
	}
	name := "opencode"
	if goos == "windows" {
		name += ".exe"
	}
	paths := []string{filepath.Join(home, ".opencode", "bin", name), filepath.Join(home, ".local", "bin", name)}
	if goos != "windows" {
		paths = append(paths, "/opt/homebrew/bin/opencode", "/usr/local/bin/opencode")
	}
	for _, p := range paths {
		if f, e := stat(p); e == nil && !f.IsDir() && (goos == "windows" || f.Mode()&0111 != 0) {
			return true
		}
	}
	return false
}

func openCodeEntry(exe string, extra []string) map[string]any {
	entry := map[string]any{"type": "local", "command": []string{exe, "--transport", "stdio"}, "enabled": true}
	env := map[string]string{}
	for i := 0; i+1 < len(extra); i += 2 {
		k, v, ok := strings.Cut(extra[i+1], "=")
		if ok {
			env[k] = v
		}
	}
	if len(env) > 0 {
		entry["environment"] = env
	}
	return entry
}

// Strict JSON only: JSONC is left byte-for-byte intact for manual editing.
func openCodeConfig(old []byte, entry map[string]any, replace bool) ([]byte, bool, error) {
	root := map[string]json.RawMessage{}
	if len(old) > 0 {
		if e := json.Unmarshal(old, &root); e != nil {
			return nil, false, e
		}
		if root == nil {
			return nil, false, errors.New("object required")
		}
	}
	servers := map[string]json.RawMessage{}
	if raw, ok := root["mcp"]; ok {
		if e := json.Unmarshal(raw, &servers); e != nil {
			return nil, false, e
		}
		if servers == nil {
			return nil, false, errors.New("object required")
		}
	}
	if _, ok := servers["servers"]; ok {
		return nil, false, errors.New("V2 configuration requires CLI")
	}
	_, exists := servers["aulaglobal"]
	if exists && !replace {
		return nil, true, nil
	}
	b, e := json.Marshal(entry)
	if e != nil {
		return nil, exists, e
	}
	servers["aulaglobal"] = b
	root["mcp"], e = json.Marshal(servers)
	if e != nil {
		return nil, exists, e
	}
	if len(old) == 0 {
		root["$schema"] = json.RawMessage(`"https://opencode.ai/config.json"`)
	}
	b, e = json.MarshalIndent(root, "", "  ")
	return append(b, '\n'), exists, e
}

func registerOpenCode(u setupUI, detected bool, dir, exe string, extra []string) {
	if !detected {
		fmt.Fprintln(u.out, "! OpenCode: no detectado.")
		return
	}
	fmt.Fprintln(u.out, "✓ OpenCode: detectado.")
	fmt.Fprint(u.out, "\n› ¿Añadir AulaGlobal a OpenCode? [S/n] ")
	answer := strings.ToLower(strings.TrimSpace(u.line()))
	if answer != "" && answer != "s" && answer != "si" && answer != "sí" {
		return
	}
	path := filepath.Join(dir, "opencode.json")
	// Never choose one of two potentially conflicting files silently.
	jsonc := filepath.Join(dir, "opencode.jsonc")
	_, ce := os.Stat(jsonc)
	_, je := os.Stat(path)
	entry := openCodeEntry(exe, extra)
	fail := func() {
		fmt.Fprintln(u.out, "✗ No se pudo actualizar OpenCode con seguridad. No se ha cambiado su configuración.")
		fmt.Fprintln(u.out, "! Alternativa manual · OpenCode V1: añade este bloque a tu configuración (en V2, usa mcp.servers y omite enabled):")
		b, _, _ := openCodeConfig(nil, entry, true)
		fmt.Fprintln(u.out, string(b))
	}
	if ce == nil {
		if je == nil {
			fail()
			return
		}
		path = jsonc
	} else if !os.IsNotExist(ce) {
		fail()
		return
	}
	old, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		fail()
		return
	}
	b, exists, e := openCodeConfig(old, entry, false)
	if e == nil && exists {
		if !u.yes("Ya existe aulaglobal. ¿Quieres reemplazarlo? [s/N]") {
			return
		}
		b, _, e = openCodeConfig(old, entry, true)
	}
	if e != nil {
		fail()
		return
	}
	if e = atomicConfig(path, b); e != nil {
		fail()
		return
	}
	fmt.Fprintln(u.out, "✓ Configuración de OpenCode guardada en:\n  "+path+"\n  Copia anterior (si existía): .bak.")
}
