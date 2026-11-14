package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type claudeRunner func(context.Context, string, ...string) ([]byte, error)

func runClaude(ctx context.Context, path string, args ...string) ([]byte, error) {
	// Invoke the standard npm entry point without a command shell on Windows.
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(path), ".cmd") {
		script := filepath.Join(filepath.Dir(path), "node_modules", "@anthropic-ai", "claude-code", "cli.js")
		if _, e := os.Stat(script); e != nil {
			return nil, e
		}
		node := filepath.Join(filepath.Dir(path), "node.exe")
		if _, e := os.Stat(node); e != nil {
			var err error
			node, err = exec.LookPath("node")
			if err != nil {
				return nil, err
			}
		}
		return exec.CommandContext(ctx, node, append([]string{script}, args...)...).CombinedOutput()
	}
	return exec.CommandContext(ctx, path, args...).CombinedOutput()
}

func findClaude(goos, home, appdata string, look func(string) (string, error), stat func(string) (os.FileInfo, error)) string {
	if p, e := look("claude"); e == nil {
		return p
	}
	paths := []string{filepath.Join(home, ".claude", "local", "claude"), filepath.Join(home, ".local", "bin", "claude"), "/opt/homebrew/bin/claude", "/usr/local/bin/claude"}
	if goos == "windows" {
		paths = []string{filepath.Join(home, ".local", "bin", "claude.exe"), filepath.Join(home, ".local", "bin", "claude.cmd"), filepath.Join(home, ".claude", "local", "claude.exe")}
		if appdata != "" {
			paths = append(paths, filepath.Join(appdata, "npm", "claude.cmd"))
		}
	}
	for _, p := range paths {
		if f, e := stat(p); e == nil && !f.IsDir() && (goos == "windows" || f.Mode()&0111 != 0) {
			return p
		}
	}
	return ""
}

func codeArgs(exe string, extra []string) []string {
	args := append([]string{"mcp", "add", "--scope", "user"}, extra...)
	return append(args, "aulaglobal", "--", exe, "--transport", "stdio")
}

// Each CLI operation has its own deadline. Never print subprocess output:
// a third party CLI may include private configuration in diagnostics.
func registerCode(u setupUI, cli, exe string, extra []string, run claudeRunner) bool {
	if cli == "" {
		fmt.Fprintln(u.out, "! Claude Code: no detectado.")
		return true
	}
	fmt.Fprintln(u.out, "✓ Claude Code: detectado.")
	fmt.Fprint(u.out, "\n› ¿Añadir AulaGlobal a Claude Code? [S/n] ")
	answer := strings.ToLower(strings.TrimSpace(u.line()))
	if answer != "" && answer != "s" && answer != "si" && answer != "sí" {
		return false
	}
	call := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return run(ctx, cli, args...)
	}
	output, err := call("mcp", "get", "aulaglobal")
	if err == nil {
		// Fail closed if the CLI does not clearly identify a user-scoped entry.
		if !strings.Contains(string(output), "User config") && !strings.Contains(string(output), "User scope") {
			fmt.Fprintln(u.out, "! Ya existe aulaglobal en otro ámbito o no se pudo identificar.\n  Revisa «claude mcp get aulaglobal» antes de añadirlo.")
			return false
		}
		if !u.yes("Ya existe aulaglobal. ¿Quieres reemplazarlo? [s/N]") {
			return false
		}
		if _, err = call("mcp", "remove", "aulaglobal", "--scope", "user"); err != nil {
			fmt.Fprintln(u.out, "✗ No se pudo quitar el registro anterior.")
			return true
		}
	} else if lower := strings.ToLower(string(output)); !strings.Contains(lower, "no mcp server named") && !strings.Contains(lower, "no mcp server found") {
		fmt.Fprintln(u.out, "✗ No se pudo comprobar el registro de Claude Code.")
		return true
	}
	if _, err = call(codeArgs(exe, extra)...); err != nil {
		fmt.Fprintln(u.out, "✗ No se pudo añadir AulaGlobal a Claude Code.")
		return true
	}
	fmt.Fprintln(u.out, "✓ AulaGlobal añadido a Claude Code para todos tus proyectos.")
	return false
}

func manualCode(u setupUI, cli, exe string, extra []string) {
	if cli == "" {
		cli = "claude"
	}
	fmt.Fprint(u.out, "\n── Alternativa manual · Claude Code ──\n\nEjecuta este comando cuando tengas Claude Code instalado:\n")
	// Single quotes are safe for POSIX shells and PowerShell, including spaces.
	quote := func(s string) string {
		if runtime.GOOS == "windows" {
			return "'" + strings.ReplaceAll(s, "'", "''") + "'"
		}
		return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
	}
	parts := []string{quote(cli)}
	if runtime.GOOS == "windows" {
		parts = append([]string{"&"}, parts...)
	}
	for _, arg := range codeArgs(exe, extra) {
		parts = append(parts, quote(arg))
	}
	fmt.Fprintln(u.out, "  "+strings.Join(parts, " "))
}
