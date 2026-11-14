package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
