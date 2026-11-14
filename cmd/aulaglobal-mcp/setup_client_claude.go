package main

import (
	"errors"
	"fmt"
	"strings"
)

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
func (a claudeAssistant) Inspect(e clientEnv, w desiredEntry) inspection {
	cli := a.cli(e)
	if cli == "" {
		return inspection{state: notInstalledHere}
	}
	b, err := e.call(e.Run, cli, "mcp", "get", "aulaglobal")
	if err != nil {
		lower := strings.ToLower(string(b))
		if strings.Contains(lower, "no mcp server named") || strings.Contains(lower, "no mcp server found") {
			return inspection{state: missing}
		}
		return inspection{err: errors.New("cannot inspect Claude")}
	}
	lines := map[string]string{}
	env := map[string]string{}
	inEnv := false
	for _, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "To remove") {
			break
		}
		if inEnv {
			if k, v, ok := strings.Cut(s, "="); ok {
				env[k] = v
			}
			continue
		}
		k, v, ok := strings.Cut(s, ":")
		if !ok {
			continue
		}
		lines[k] = strings.TrimSpace(v)
		if k == "Environment" {
			inEnv = true
		}
	}
	scope := ""
	for _, v := range []struct{ prefix, scope string }{{"Local config", "local"}, {"Project config", "project"}, {"User config", "user"}, {"User scope", "user"}} {
		if strings.HasPrefix(lines["Scope"], v.prefix) {
			scope = v.scope
		}
	}
	if scope == "" {
		return inspection{err: errors.New("unknown Claude scope")}
	}
	if scope == "local" {
		return inspection{state: otherScope, scope: scope, detail: "configurado solo en este proyecto (local). Se moverá a usuario para todos tus proyectos."}
	}
	if scope == "project" {
		return inspection{state: otherScope, scope: scope, detail: "definido por este proyecto en .mcp.json; ese archivo se conservará."}
	}
	wantEnv, _ := w.entry()["env"].(map[string]string)
	if wantEnv == nil {
		wantEnv = map[string]string{}
	}
	if lines["Type"] == "stdio" && lines["Command"] == w.Exe && lines["Args"] == "--transport stdio" && sameEntry(env, wantEnv) {
		return inspection{state: identical, scope: scope}
	}
	return inspection{state: different, scope: scope, detail: "comando " + fmt.Sprintf("%q", lines["Command"])}
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
	// Same process cwd for get/remove/add: local entries belong to this project.
	if i.scope == "local" || i.scope == "user" {
		if _, err := e.call(e.Run, cli, "mcp", "remove", "aulaglobal", "--scope", i.scope); err != nil {
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
