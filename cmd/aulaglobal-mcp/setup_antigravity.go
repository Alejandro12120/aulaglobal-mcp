package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"aulaglobal-mcp/internal/userpaths"
)

// All filesystem probes are injectable; simulated OS tests use fake metadata.
func findAntigravity(goos, home, local string, look func(string) (string, error), stat func(string) (os.FileInfo, error)) (string, bool) {
	detected := false
	for _, name := range []string{"agy", "antigravity"} {
		if p, e := look(name); e == nil {
			if name == "agy" {
				return p, true
			}
			detected = true
		}
	}
	dirs := []string{filepath.Join(home, ".gemini", "antigravity"), filepath.Dir(userpaths.Antigravity(home))}
	apps := []string{"/usr/share/antigravity/antigravity", "/opt/Antigravity/Antigravity", filepath.Join(home, ".local", "bin", "agy")}
	if goos == "darwin" {
		dirs = append(dirs, "/Applications/Antigravity.app", filepath.Join(home, "Applications", "Antigravity.app"))
		apps = []string{filepath.Join(home, ".local", "bin", "agy")}
	}
	if goos == "windows" {
		apps = []string{filepath.Join(local, "Programs", "Antigravity", "Antigravity.exe"), filepath.Join(local, "agy", "bin", "agy.exe"), filepath.Join(home, "AppData", "Local", "Programs", "Antigravity", "Antigravity.exe")}
	}
	for _, p := range apps {
		if f, e := stat(p); e == nil && !f.IsDir() && (goos == "windows" || f.Mode()&0111 != 0) {
			if filepath.Base(p) == "agy" || filepath.Base(p) == "agy.exe" {
				return p, true
			}
			detected = true
		}
	}
	for _, p := range dirs {
		if f, e := stat(p); e == nil && f.IsDir() {
			detected = true
		}
	}
	return "", detected
}

// Reject duplicate keys too: encoding/json otherwise silently drops content.
func strictAntigravityJSON(b []byte) error {
	d := json.NewDecoder(strings.NewReader(string(b)))
	var value func() error
	value = func() error {
		tok, e := d.Token()
		if e != nil {
			return e
		}
		if delim, ok := tok.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					s, ok := k.(string)
					if !ok || seen[s] {
						return errors.New("duplicate key")
					}
					seen[s] = true
					if e = value(); e != nil {
						return e
					}
				}
			case '[':
				for d.More() {
					if e = value(); e != nil {
						return e
					}
				}
			default:
				return errors.New("invalid delimiter")
			}
			_, e = d.Token()
			return e
		}
		return nil
	}
	if e := value(); e != nil {
		return e
	}
	if d.More() {
		return errors.New("extra JSON")
	}
	return nil
}

func antigravityConfig(old []byte, entry map[string]any, replace bool) ([]byte, bool, error) {
	if len(old) > 0 {
		if e := strictAntigravityJSON(old); e != nil {
			return nil, false, e
		}
	}
	var root map[string]json.RawMessage
	if len(old) > 0 {
		if e := json.Unmarshal(old, &root); e != nil {
			return nil, false, e
		}
		if root == nil {
			return nil, false, errors.New("object required")
		}
	}
	var servers map[string]json.RawMessage
	if raw, ok := root["mcpServers"]; ok {
		if e := json.Unmarshal(raw, &servers); e != nil {
			return nil, false, e
		}
		if servers == nil {
			return nil, false, errors.New("object required")
		}
	}
	_, exists := servers["aulaglobal"]
	if exists && !replace {
		return nil, true, nil
	}
	b, e := claudeConfig(old, entry)
	return b, exists, e
}

// The CLI generates its entry in an isolated home. Only our safe merge writes
// the real config, preserving unknown keys and providing atomic writes + backup.
func antigravityCLIEntry(cli, exe string, extra []string) (map[string]any, error) {
	home, e := os.MkdirTemp("", "aulaglobal-agy-*")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(home)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	call := func(args ...string) ([]byte, error) {
		c := exec.CommandContext(ctx, cli, args...)
		for _, v := range os.Environ() {
			k, _, _ := strings.Cut(v, "=")
			if !strings.EqualFold(k, "HOME") && !strings.EqualFold(k, "USERPROFILE") {
				c.Env = append(c.Env, v)
			}
		}
		c.Env = append(c.Env, "HOME="+home, "USERPROFILE="+home)
		c.Dir = home
		return c.CombinedOutput()
	}
	help, e := call("mcp", "add", "--help")
	if e != nil || !strings.Contains(string(help), "--env") || !strings.Contains(string(help), "<commandOrUrl>") {
		return nil, errors.New("unsupported CLI")
	}
	args := []string{"mcp", "add"}
	args = append(args, extra...)
	args = append(args, "aulaglobal", exe, "--transport", "stdio")
	if _, e = call(args...); e != nil {
		return nil, e
	}
	b, e := os.ReadFile(userpaths.Antigravity(home))
	if e != nil {
		return nil, e
	}
	var root struct {
		Servers map[string]map[string]any `json:"mcpServers"`
	}
	if e = json.Unmarshal(b, &root); e != nil {
		return nil, e
	}
	entry := root.Servers["aulaglobal"]
	if entry == nil {
		return nil, errors.New("missing entry")
	}
	return entry, nil
}

func registerAntigravity(u setupUI, detected bool, cli, path, exe string, extra []string) {
	if !detected {
		fmt.Fprintln(u.out, "! Antigravity: no detectado.")
		return
	}
	fmt.Fprintln(u.out, "✓ Antigravity: detectado.")
	fmt.Fprint(u.out, "\n› ¿Añadir AulaGlobal a Antigravity? [S/n] ")
	a := strings.ToLower(strings.TrimSpace(u.line()))
	if a != "" && a != "s" && a != "si" && a != "sí" {
		return
	}
	entry := map[string]any{"command": exe, "args": []string{"--transport", "stdio"}}
	env := openCodeEntry(exe, extra)["environment"]
	if env != nil {
		entry["env"] = env
	}
	fail := func() {
		fmt.Fprintln(u.out, "✗ No se pudo actualizar Antigravity con seguridad. No se ha cambiado la configuración.")
		fmt.Fprintln(u.out, "! Alternativa manual: añade este bloque desde Manage MCP Servers → View raw config:")
		b, _, _ := antigravityConfig(nil, entry, true)
		fmt.Fprintln(u.out, string(b))
	}
	old, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		fail()
		return
	}
	b, exists, e := antigravityConfig(old, entry, false)
	if e != nil {
		fail()
		return
	}
	if exists {
		if !u.yes("Ya existe aulaglobal. ¿Quieres reemplazarlo? [s/N]") {
			return
		}
		b, _, e = antigravityConfig(old, entry, true)
	}
	if e != nil {
		fail()
		return
	}
	if cli != "" {
		generated, err := antigravityCLIEntry(cli, exe, extra)
		if err != nil {
			fail()
			return
		}
		b, _, e = antigravityConfig(old, generated, true)
		if e != nil {
			fail()
			return
		}
	}
	if e = atomicConfig(path, b); e != nil {
		fail()
		return
	}
	fmt.Fprintln(u.out, "✓ Configuración de Antigravity guardada en:\n  "+path+"\n  Copia anterior (si existía): .bak.")
	fmt.Fprintln(u.out, "! Recarga los servidores en /mcp (CLI) o usa Restart en Customizations → Installed → MCP Servers; en el IDE, vuelve a abrir o reinicia Antigravity.")
}
