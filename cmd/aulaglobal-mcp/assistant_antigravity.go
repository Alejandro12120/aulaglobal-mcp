package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"aulaglobal-mcp/internal/userpaths"
)

// Antigravity keeps its servers in a JSON file like Claude Desktop does. When
// its `agy` CLI is installed, the entry comes from the CLI so that it has the
// shape this agy version expects.

func antigravityAssistant() assistant {
	return fileAssistant{
		id:     "antigravity",
		name:   "Antigravity",
		reload: "Recarga /mcp en CLI; usa Restart en Customizations → Installed → MCP Servers, o reinicia el IDE.",
		path:   func(e clientEnv) (string, error) { return userpaths.Antigravity(e.Home), nil },
		detect: func(e clientEnv) bool {
			_, ok := findAntigravity(e.GOOS, e.Home, e.LocalAppData, e.Look, e.Stat)
			return ok
		},
		merge:      mcpServersConfig,
		entryOf:    mcpServersEntry,
		removeFrom: mcpServersRemove,
		generate: func(e clientEnv, w desiredEntry) (map[string]any, error) {
			cli, _ := findAntigravity(e.GOOS, e.Home, e.LocalAppData, e.Look, e.Stat)
			if cli != "" {
				return e.GenerateAG(cli, w.Exe, w.Extra)
			}
			return w.entry(), nil
		},
		ignoreDisabledFalse: true,
	}
}

// findAntigravity returns the path of the `agy` CLI, if any, and whether
// Antigravity is installed at all (the app, its folders or the CLI). Every
// file system probe is a parameter so tests can simulate other systems.
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
		if isExecutable(goos, stat, p) {
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

// antigravityCLIEntry runs `agy mcp add` in a throwaway home directory and
// returns the entry it wrote. The real configuration is only ever written by
// our own merge, which keeps unknown keys and makes a backup.
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
	args = append(args, aulaglobalKey, exe, "--transport", "stdio")
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
	entry := root.Servers[aulaglobalKey]
	if entry == nil {
		return nil, errors.New("missing entry")
	}
	return entry, nil
}
