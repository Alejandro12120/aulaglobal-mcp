package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Claude Code is configured through its own CLI (`claude mcp ...`), not by
// editing its configuration file.
type claudeAssistant struct{}

func (claudeAssistant) ID() string   { return "claude-code" }
func (claudeAssistant) Name() string { return "Claude Code" }
func (claudeAssistant) Reload() string {
	return "Abre una sesión nueva y usa /mcp para comprobar la conexión."
}
func (claudeAssistant) cli(e clientEnv) string {
	return findClaude(e.GOOS, e.Home, e.AppData, e.Look, e.Stat)
}
func (a claudeAssistant) Detect(e clientEnv) bool { return a.cli(e) != "" }

func runClaude(ctx context.Context, path string, args ...string) ([]byte, error) {
	// On Windows the npm shim is a .cmd file. Run its script with node directly
	// so no command shell parses the arguments.
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
		if isExecutable(goos, stat, p) {
			return p
		}
	}
	return ""
}

// claudeEntry is the output of `claude mcp get aulaglobal`: its key/value
// lines, the Environment block and the scope ("local", "project", "user" or "").
type claudeEntry struct {
	fields, env map[string]string
	scope       string
}

// parseClaudeGet reads the output of `claude mcp get`.
func parseClaudeGet(output string) claudeEntry {
	c := claudeEntry{fields: map[string]string{}, env: map[string]string{}}
	inEnv := false
	for _, line := range strings.Split(output, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "To remove") {
			break
		}
		if inEnv {
			if k, v, ok := strings.Cut(s, "="); ok {
				c.env[k] = v
			}
			continue
		}
		k, v, ok := strings.Cut(s, ":")
		if !ok {
			continue
		}
		c.fields[k] = strings.TrimSpace(v)
		if k == "Environment" {
			inEnv = true
		}
	}
	for _, v := range []struct{ prefix, scope string }{{"Local config", "local"}, {"Project config", "project"}, {"User config", "user"}, {"User scope", "user"}} {
		if strings.HasPrefix(c.fields["Scope"], v.prefix) {
			c.scope = v.scope
		}
	}
	return c
}

// get asks Claude for the aulaglobal entry. found is false when Claude says
// there is no such server. Any other failure is an error.
func (a claudeAssistant) get(e clientEnv, cli string) (entry claudeEntry, found bool, err error) {
	b, err := e.call(e.Run, cli, "mcp", "get", aulaglobalKey)
	if err != nil {
		lower := strings.ToLower(string(b))
		if strings.Contains(lower, "no mcp server named") || strings.Contains(lower, "no mcp server found") {
			return claudeEntry{}, false, nil
		}
		return claudeEntry{}, false, err
	}
	return parseClaudeGet(string(b)), true, nil
}

func (a claudeAssistant) Inspect(e clientEnv, w desiredEntry) inspection {
	cli := a.cli(e)
	if cli == "" {
		return inspection{state: notInstalledHere}
	}
	c, found, err := a.get(e, cli)
	if err != nil {
		return inspection{err: errors.New("cannot inspect Claude")}
	}
	if !found {
		return inspection{state: missing}
	}
	if c.scope == "" {
		return inspection{err: errors.New("unknown Claude scope")}
	}
	if c.scope == "local" {
		return inspection{state: otherScope, scope: c.scope, detail: "configurado solo en este proyecto (local). Se moverá a usuario para todos tus proyectos."}
	}
	if c.scope == "project" {
		return inspection{state: otherScope, scope: c.scope, detail: "definido por este proyecto en .mcp.json; ese archivo se conservará."}
	}
	wantEnv, _ := w.entry()["env"].(map[string]string)
	if wantEnv == nil {
		wantEnv = map[string]string{}
	}
	if c.fields["Type"] == "stdio" && c.fields["Command"] == w.Exe && c.fields["Args"] == "--transport stdio" && sameEntry(c.env, wantEnv) {
		return inspection{state: identical, scope: c.scope}
	}
	return inspection{state: different, scope: c.scope, detail: "comando " + fmt.Sprintf("%q", c.fields["Command"])}
}

func (a claudeAssistant) Apply(e clientEnv, w desiredEntry, replace bool) error {
	i := a.Inspect(e, w)
	if i.err != nil {
		return i.err
	}
	if i.state == identical {
		return nil
	}
	if i.state == notInstalledHere {
		return errors.New("not installed")
	}
	if i.state != missing && !replace {
		return errors.New("replacement not authorized")
	}
	cli := a.cli(e)
	// get, remove and add must run in the same working directory: a local entry
	// belongs to the project the process runs in.
	if i.scope == "local" || i.scope == "user" {
		if _, err := e.call(e.Run, cli, "mcp", "remove", aulaglobalKey, "--scope", i.scope); err != nil {
			return errors.New("remove failed")
		}
	}
	_, err := e.call(e.Run, cli, codeArgs(w.Exe, w.Extra)...)
	return err
}

func (a claudeAssistant) Manual(w desiredEntry) string {
	var out strings.Builder
	manualCode(setupUI{out: &out}, "claude", w.Exe, w.Extra)
	return strings.TrimSpace(out.String())
}

func (a claudeAssistant) Find(e clientEnv) link {
	cli := a.cli(e)
	if cli == "" {
		return link{state: linkNotInstalled}
	}
	c, found, err := a.get(e, cli)
	if err != nil {
		return link{state: linkError, detail: "no se puede consultar a Claude Code"}
	}
	if !found {
		return link{state: linkNone}
	}
	command := c.fields["Command"]
	switch c.scope {
	case "user":
		return link{state: linkPresent, command: command, env: c.env, detail: "claude mcp remove --scope user aulaglobal"}
	case "local":
		return link{state: linkElsewhere, command: command, detail: "está configurado solo para un proyecto (ámbito local). No se toca; para quitarlo, ejecuta en esa carpeta: claude mcp remove --scope local aulaglobal"}
	case "project":
		return link{state: linkElsewhere, command: command, detail: "lo define un proyecto en su .mcp.json. No se toca ese archivo; si no lo quieres, bórralo tú de allí."}
	}
	return link{state: linkError, detail: "ámbito de Claude Code desconocido"}
}

func (a claudeAssistant) Remove(e clientEnv) error {
	l := a.Find(e)
	switch l.state {
	case linkNone:
		return nil
	case linkPresent:
	default:
		return errors.New("not removable")
	}
	out, err := e.call(e.Run, a.cli(e), "mcp", "remove", "--scope", "user", aulaglobalKey)
	if err != nil && !strings.Contains(strings.ToLower(string(out)), "no mcp server") {
		return errors.New("remove failed")
	}
	if after := a.Find(e); after.state == linkPresent {
		return errors.New("entry still present")
	}
	return nil
}

func (a claudeAssistant) ManualRemove(clientEnv) string {
	return "Alternativa manual · Claude Code: ejecuta  claude mcp remove --scope user aulaglobal"
}

func codeArgs(exe string, extra []string) []string {
	args := append([]string{"mcp", "add", "--scope", "user", aulaglobalKey}, extra...)
	return append(args, "--", exe, "--transport", "stdio")
}

// manualCode prints the command that adds the server to Claude Code by hand.
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
