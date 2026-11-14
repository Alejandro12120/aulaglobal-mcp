package main

import (
	"aulaglobal-mcp/internal/userpaths"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type openCodeAssistant struct{}

func (openCodeAssistant) ID() string     { return "opencode" }
func (openCodeAssistant) Name() string   { return "OpenCode" }
func (openCodeAssistant) Reload() string { return "Reinicia OpenCode o abre una sesión nueva." }
func (openCodeAssistant) cli(e clientEnv) string {
	return findOpenCode(e.GOOS, e.Home, e.AppData, e.Look, e.Stat)
}
func (a openCodeAssistant) Detect(e clientEnv) bool {
	return a.cli(e) != "" || dirExists(e, userpaths.OpenCode(e.Home, e.XDG))
}
func (a openCodeAssistant) path(e clientEnv) (string, error) {
	dir := userpaths.OpenCode(e.Home, e.XDG)
	p := filepath.Join(dir, "opencode.json")
	c := filepath.Join(dir, "opencode.jsonc")
	_, ce := e.Stat(c)
	_, je := e.Stat(p)
	if ce == nil {
		if je == nil {
			return "", errors.New("conflicting files")
		}
		if !os.IsNotExist(je) {
			return "", je
		}
		return c, nil
	}
	if !os.IsNotExist(ce) {
		return "", ce
	}
	return p, nil
}
func (a openCodeAssistant) read(e clientEnv, w desiredEntry) (string, []byte, inspection) {
	if !a.Detect(e) {
		return "", nil, inspection{state: notInstalledHere}
	}
	p, err := a.path(e)
	if err != nil {
		return p, nil, inspection{err: err}
	}
	old, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return p, nil, inspection{err: err}
	}
	if len(old) > 0 {
		if err = strictAntigravityJSON(old); err != nil {
			return p, old, inspection{err: err}
		}
	}
	_, exists, err := openCodeConfig(old, openCodeEntry(w.Exe, w.Extra), false)
	if err != nil {
		return p, old, inspection{err: err}
	}
	if !exists {
		return p, old, inspection{state: missing}
	}
	var root map[string]any
	json.Unmarshal(old, &root)
	m, _ := root["mcp"].(map[string]any)
	v1 := true
	if servers, ok := m["servers"].(map[string]any); ok {
		m = servers
		v1 = false
	}
	want := openCodeEntry(w.Exe, w.Extra)
	if v1 {
		want["enabled"] = true
	}
	if sameEntry(m["aulaglobal"], want) {
		return p, old, inspection{state: identical}
	}
	detail := "entrada existente"
	if b, ok := m["aulaglobal"].(map[string]any); ok {
		if cmd, ok := b["command"].([]any); ok && len(cmd) > 0 {
			v, _ := json.Marshal(cmd[0])
			detail = "comando " + string(v)
		}
	}
	return p, old, inspection{state: different, detail: detail}
}
func (a openCodeAssistant) Inspect(e clientEnv, w desiredEntry) inspection {
	_, _, i := a.read(e, w)
	return i
}
func (a openCodeAssistant) Apply(e clientEnv, w desiredEntry, replace bool) error {
	p, old, i := a.read(e, w)
	if i.err != nil {
		return i.err
	}
	if i.state == identical {
		return nil
	}
	if i.state == notInstalledHere {
		return errors.New("not installed")
	}
	if i.state == different && !replace {
		return errors.New("replacement not authorized")
	}
	cli := a.cli(e)
	scriptable := false
	if cli != "" {
		help, err := e.call(e.OpenRun, cli, "mcp", "add", "--help")
		scriptable = err == nil && strings.Contains(string(help), "--global")
	}
	b, _, err := openCodeConfig(old, openCodeEntry(w.Exe, w.Extra), replace)
	if err != nil {
		return err
	}
	if cli != "" && scriptable && i.state == missing {
		if _, err = e.call(e.OpenRun, cli, openCodeArgs(w.Exe, w.Extra)...); err != nil {
			return err
		}
		// Verify the generated global entry, not merely CLI exit status.
		_, _, check := a.read(e, w)
		if check.err != nil {
			return check.err
		}
		if check.state != identical {
			return errors.New("unverified global entry")
		}
		return nil
	}
	if !dirExists(e, filepath.Dir(p)) {
		return errors.New("global directory missing")
	}
	return e.write(p, b)
}
func (a openCodeAssistant) Manual(w desiredEntry) string {
	v2, _, _ := openCodeConfig(nil, openCodeEntry(w.Exe, w.Extra), true)
	v1, _, _ := openCodeConfig([]byte(`{"mcp":{"aulaglobal":{"type":"local","command":["old"]}}}`), openCodeEntry(w.Exe, w.Extra), true)
	return "Alternativa manual · OpenCode:\nV2 actual: mcp.servers, sin enabled.\n" + string(v2) + "V1 antiguo: mcp, con enabled.\n" + string(v1)
}
