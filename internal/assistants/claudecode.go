package assistants

import (
	"context"
	"errors"
	"fmt"
	"io"
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
func (claudeAssistant) cli(env Env) string {
	return findClaude(env.GOOS, env.Home, env.AppData, env.LookPath, env.Stat)
}
func (a claudeAssistant) Detect(env Env) bool { return a.cli(env) != "" }

func runClaude(ctx context.Context, cliPath string, args ...string) ([]byte, error) {
	// On Windows the npm shim is a .cmd file. Run its script with node directly
	// so no command shell parses the arguments.
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(cliPath), ".cmd") {
		script := filepath.Join(filepath.Dir(cliPath), "node_modules", "@anthropic-ai", "claude-code", "cli.js")
		if _, err := os.Stat(script); err != nil {
			return nil, err
		}
		node := filepath.Join(filepath.Dir(cliPath), "node.exe")
		if _, err := os.Stat(node); err != nil {
			node, err = exec.LookPath("node")
			if err != nil {
				return nil, err
			}
		}
		return exec.CommandContext(ctx, node, append([]string{script}, args...)...).CombinedOutput()
	}
	return exec.CommandContext(ctx, cliPath, args...).CombinedOutput()
}

func findClaude(goos, home, appData string, lookPath func(string) (string, error), stat func(string) (os.FileInfo, error)) string {
	if found, err := lookPath("claude"); err == nil {
		return found
	}
	candidates := []string{filepath.Join(home, ".claude", "local", "claude"), filepath.Join(home, ".local", "bin", "claude"), "/opt/homebrew/bin/claude", "/usr/local/bin/claude"}
	if goos == "windows" {
		candidates = []string{filepath.Join(home, ".local", "bin", "claude.exe"), filepath.Join(home, ".local", "bin", "claude.cmd"), filepath.Join(home, ".claude", "local", "claude.exe")}
		if appData != "" {
			candidates = append(candidates, filepath.Join(appData, "npm", "claude.cmd"))
		}
	}
	for _, candidate := range candidates {
		if isExecutable(goos, stat, candidate) {
			return candidate
		}
	}
	return ""
}

// claudeServer is the output of `claude mcp get aulaglobal`: its key/value
// lines, the Environment block and the scope ("local", "project", "user" or "").
type claudeServer struct {
	fields, environment map[string]string
	scope               string
}

// parseClaudeGet reads the output of `claude mcp get`.
func parseClaudeGet(output string) claudeServer {
	server := claudeServer{fields: map[string]string{}, environment: map[string]string{}}
	inEnvironment := false
	for _, line := range strings.Split(output, "\n") {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, "To remove") {
			break
		}
		if inEnvironment {
			if name, value, ok := strings.Cut(text, "="); ok {
				server.environment[name] = value
			}
			continue
		}
		name, value, ok := strings.Cut(text, ":")
		if !ok {
			continue
		}
		server.fields[name] = strings.TrimSpace(value)
		if name == "Environment" {
			inEnvironment = true
		}
	}
	for _, known := range []struct{ prefix, scope string }{{"Local config", "local"}, {"Project config", "project"}, {"User config", "user"}, {"User scope", "user"}} {
		if strings.HasPrefix(server.fields["Scope"], known.prefix) {
			server.scope = known.scope
		}
	}
	return server
}

// get asks Claude for the aulaglobal entry. found is false when Claude says
// there is no such server. Any other failure is an error.
func (a claudeAssistant) get(env Env, cli string) (server claudeServer, found bool, err error) {
	output, err := env.call(env.RunClaude, cli, "mcp", "get", aulaglobalKey)
	if err != nil {
		lower := strings.ToLower(string(output))
		if strings.Contains(lower, "no mcp server named") || strings.Contains(lower, "no mcp server found") {
			return claudeServer{}, false, nil
		}
		return claudeServer{}, false, err
	}
	return parseClaudeGet(string(output)), true, nil
}

func (a claudeAssistant) Inspect(env Env, want Entry) Inspection {
	cli := a.cli(env)
	if cli == "" {
		return Inspection{State: NotInstalled}
	}
	server, found, err := a.get(env, cli)
	if err != nil {
		return Inspection{Err: errors.New("cannot inspect Claude")}
	}
	if !found {
		return Inspection{State: Missing}
	}
	if server.scope == "" {
		return Inspection{Err: errors.New("unknown Claude scope")}
	}
	if server.scope == "local" {
		return Inspection{State: OtherScope, Scope: server.scope, Detail: "configurado solo en este proyecto (local). Se moverá a usuario para todos tus proyectos."}
	}
	if server.scope == "project" {
		return Inspection{State: OtherScope, Scope: server.scope, Detail: "definido por este proyecto en .mcp.json; ese archivo se conservará."}
	}
	wantEnv, _ := want.Config()["env"].(map[string]string)
	if wantEnv == nil {
		wantEnv = map[string]string{}
	}
	if server.fields["Type"] == "stdio" && server.fields["Command"] == want.Exe && server.fields["Args"] == "--transport stdio" && sameEntry(server.environment, wantEnv) {
		return Inspection{State: Identical, Scope: server.scope}
	}
	return Inspection{State: Different, Scope: server.scope, Detail: "comando " + fmt.Sprintf("%q", server.fields["Command"])}
}

func (a claudeAssistant) Apply(env Env, want Entry, replace bool) error {
	inspection := a.Inspect(env, want)
	if inspection.Err != nil {
		return inspection.Err
	}
	if inspection.State == Identical {
		return nil
	}
	if inspection.State == NotInstalled {
		return errors.New("not installed")
	}
	if inspection.State != Missing && !replace {
		return errors.New("replacement not authorized")
	}
	cli := a.cli(env)
	// get, remove and add must run in the same working directory: a local entry
	// belongs to the project the process runs in.
	if inspection.Scope == "local" || inspection.Scope == "user" {
		if _, err := env.call(env.RunClaude, cli, "mcp", "remove", aulaglobalKey, "--scope", inspection.Scope); err != nil {
			return errors.New("remove failed")
		}
	}
	_, err := env.call(env.RunClaude, cli, codeArgs(want.Exe, want.Extra)...)
	return err
}

func (a claudeAssistant) Manual(want Entry) string {
	var out strings.Builder
	manualCode(&out, "claude", want.Exe, want.Extra)
	return strings.TrimSpace(out.String())
}

func (a claudeAssistant) Find(env Env) Link {
	cli := a.cli(env)
	if cli == "" {
		return Link{State: LinkNotInstalled}
	}
	server, found, err := a.get(env, cli)
	if err != nil {
		return Link{State: LinkError, Detail: "no se puede consultar a Claude Code"}
	}
	if !found {
		return Link{State: LinkNone}
	}
	command := server.fields["Command"]
	switch server.scope {
	case "user":
		return Link{State: LinkPresent, Command: command, Env: server.environment, Detail: "claude mcp remove --scope user aulaglobal"}
	case "local":
		return Link{State: LinkElsewhere, Command: command, Detail: "está configurado solo para un proyecto (ámbito local). No se toca; para quitarlo, ejecuta en esa carpeta: claude mcp remove --scope local aulaglobal"}
	case "project":
		return Link{State: LinkElsewhere, Command: command, Detail: "lo define un proyecto en su .mcp.json. No se toca ese archivo; si no lo quieres, bórralo tú de allí."}
	}
	return Link{State: LinkError, Detail: "ámbito de Claude Code desconocido"}
}

func (a claudeAssistant) Remove(env Env) error {
	link := a.Find(env)
	switch link.State {
	case LinkNone:
		return nil
	case LinkPresent:
	default:
		return errors.New("not removable")
	}
	output, err := env.call(env.RunClaude, a.cli(env), "mcp", "remove", "--scope", "user", aulaglobalKey)
	if err != nil && !strings.Contains(strings.ToLower(string(output)), "no mcp server") {
		return errors.New("remove failed")
	}
	if after := a.Find(env); after.State == LinkPresent {
		return errors.New("entry still present")
	}
	return nil
}

func (a claudeAssistant) ManualRemove(Env) string {
	return "Alternativa manual · Claude Code: ejecuta  claude mcp remove --scope user aulaglobal"
}

func codeArgs(exe string, extra []string) []string {
	args := append([]string{"mcp", "add", "--scope", "user", aulaglobalKey}, extra...)
	return append(args, "--", exe, "--transport", "stdio")
}

// manualCode prints the command that adds the server to Claude Code by hand.
func manualCode(out io.Writer, cli, exe string, extra []string) {
	if cli == "" {
		cli = "claude"
	}
	fmt.Fprint(out, "\n── Alternativa manual · Claude Code ──\n\nEjecuta este comando cuando tengas Claude Code instalado:\n")
	// Single quotes are safe for POSIX shells and PowerShell, including spaces.
	quote := func(text string) string {
		if runtime.GOOS == "windows" {
			return "'" + strings.ReplaceAll(text, "'", "''") + "'"
		}
		return "'" + strings.ReplaceAll(text, "'", "'\"'\"'") + "'"
	}
	parts := []string{quote(cli)}
	if runtime.GOOS == "windows" {
		parts = append([]string{"&"}, parts...)
	}
	for _, arg := range codeArgs(exe, extra) {
		parts = append(parts, quote(arg))
	}
	fmt.Fprintln(out, "  "+strings.Join(parts, " "))
}
