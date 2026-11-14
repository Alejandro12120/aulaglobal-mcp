package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func findOpenCode(goos, home, appdata string, look func(string) (string, error), stat func(string) (os.FileInfo, error)) string {
	names := []string{"opencode"}
	if goos == "windows" {
		names = []string{"opencode.exe", "opencode.cmd"}
	}
	for _, n := range names {
		if p, e := look(n); e == nil {
			return p
		}
	}
	var paths []string
	for _, n := range names {
		for _, d := range []string{filepath.Join(home, ".opencode", "bin"), filepath.Join(home, ".local", "bin"), filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".npm", "bin")} {
			paths = append(paths, filepath.Join(d, n))
		}
		if goos == "windows" && appdata != "" {
			paths = append(paths, filepath.Join(appdata, "npm", n))
		}
	}
	if goos != "windows" {
		paths = append(paths, "/opt/homebrew/bin/opencode", "/usr/local/bin/opencode", "/usr/bin/opencode")
	}
	for _, p := range paths {
		if f, e := stat(p); e == nil && !f.IsDir() && (goos == "windows" || f.Mode()&0111 != 0) {
			return p
		}
	}
	return ""
}

func runOpenCode(ctx context.Context, path string, args ...string) ([]byte, error) {
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(path), ".cmd") {
		script := filepath.Join(filepath.Dir(path), "node_modules", "opencode-ai", "bin", "opencode")
		if _, e := os.Stat(script); e != nil {
			return nil, e
		}
		node, e := exec.LookPath("node")
		if e != nil {
			return nil, e
		}
		return exec.CommandContext(ctx, node, append([]string{script}, args...)...).CombinedOutput()
	}
	return exec.CommandContext(ctx, path, args...).CombinedOutput()
}

func openCodeArgs(exe string, extra []string) []string {
	args := []string{"mcp", "add", "aulaglobal", "--global"}
	for i := 0; i+1 < len(extra); i += 2 {
		k, _, ok := strings.Cut(extra[i+1], "=")
		if ok && (k == "AULAGLOBAL_TOKEN_FILE" || k == "AULAGLOBAL_DOWNLOAD_DIR") {
			args = append(args, "--env", extra[i+1])
		}
	}
	return append(args, "--", exe, "--transport", "stdio")
}

func openCodeEntry(exe string, extra []string) map[string]any {
	entry := map[string]any{"type": "local", "command": []string{exe, "--transport", "stdio"}}
	env := map[string]string{}
	for i := 0; i+1 < len(extra); i += 2 {
		k, v, ok := strings.Cut(extra[i+1], "=")
		if ok && (k == "AULAGLOBAL_TOKEN_FILE" || k == "AULAGLOBAL_DOWNLOAD_DIR") {
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
	if len(strings.TrimSpace(string(old))) > 0 {
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
	mcp := servers
	v1 := false
	if raw, ok := mcp["servers"]; ok {
		servers = map[string]json.RawMessage{}
		if e := json.Unmarshal(raw, &servers); e != nil || servers == nil {
			return nil, false, errors.New("invalid servers")
		}
		for k, raw := range mcp {
			if k != "servers" {
				var v map[string]any
				if json.Unmarshal(raw, &v) == nil && v["type"] != nil {
					return nil, false, errors.New("mixed versions")
				}
			}
		}
	} else {
		for _, raw := range mcp {
			var v map[string]any
			if json.Unmarshal(raw, &v) == nil && v["type"] != nil {
				v1 = true
			}
		}
		if !v1 && len(mcp) > 0 {
			return nil, false, errors.New("ambiguous mcp")
		}
		if !v1 {
			servers = map[string]json.RawMessage{}
		}
	}
	copyEntry := map[string]any{}
	for k, v := range entry {
		copyEntry[k] = v
	}
	if v1 {
		copyEntry["enabled"] = true
	} else {
		delete(copyEntry, "enabled")
	}
	_, exists := servers["aulaglobal"]
	if exists && !replace {
		return nil, true, nil
	}
	b, e := json.Marshal(copyEntry)
	if e != nil {
		return nil, exists, e
	}
	servers["aulaglobal"] = b
	if !v1 {
		mcp["servers"], e = json.Marshal(servers)
	} else {
		mcp = servers
	}
	root["mcp"], e = json.Marshal(mcp)
	if e != nil {
		return nil, exists, e
	}
	if len(root) == 1 {
		root["$schema"] = json.RawMessage(`"https://opencode.ai/config.json"`)
	}
	b, e = json.MarshalIndent(root, "", "  ")
	return append(b, '\n'), exists, e
}

func registerOpenCode(u setupUI, cli, dir, exe string, extra []string, run claudeRunner) {
	_, dirErr := os.Stat(dir)
	if cli == "" && dirErr != nil {
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
		fmt.Fprintln(u.out, "✗ No se pudo actualizar OpenCode con seguridad. Revisa la configuración antes de reintentarlo.")
		fmt.Fprintln(u.out, "! Alternativa manual: elige el bloque que corresponda a tu versión de OpenCode:")
		b, _, _ := openCodeConfig(nil, entry, true)
		fmt.Fprintln(u.out, "V2 actual: mcp.servers, sin enabled.")
		fmt.Fprintln(u.out, string(b))
		b, _, _ = openCodeConfig([]byte(`{"mcp":{"aulaglobal":{"type":"local","command":["anterior"]}}}`), entry, true)
		fmt.Fprintln(u.out, "V1 antiguo: mcp, con enabled.")
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
	call := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return run(ctx, cli, args...)
	}
	scriptable := false
	if cli != "" {
		help, err := call("mcp", "add", "--help")
		scriptable = err == nil && strings.Contains(string(help), "--global")
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
	if scriptable && !exists {
		if _, err := call(openCodeArgs(exe, extra)...); err != nil {
			fail()
			return
		}
		verified, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			verified, err = os.ReadFile(jsonc)
		}
		_, found, parseErr := openCodeConfig(verified, entry, false)
		if err != nil || parseErr != nil || !found {
			fmt.Fprintln(u.out, "✗ No se pudo verificar el registro global. Revisa opencode.json(c).")
			return
		}
		fmt.Fprintln(u.out, "✓ AulaGlobal añadido a OpenCode para todos tus proyectos.")
		return
	}
	// Replace only this entry with the safe merge; no remove/add gap.
	if cli != "" && !scriptable && dirErr != nil {
		fail()
		return
	}
	if e = atomicConfig(path, b); e != nil {
		fail()
		return
	}
	fmt.Fprintln(u.out, "✓ Configuración de OpenCode guardada en:\n  "+path+"\n  Copia anterior (si existía): .bak.")
}
