package assistants

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"aulaglobal-mcp/internal/userpaths"
)

// OpenCode keeps its servers in opencode.json (or opencode.jsonc) in two
// formats: V2 under mcp.servers and the older V1 directly under mcp. When its
// CLI can add a global server, setup uses it; otherwise it edits the file.
type openCodeAssistant struct{}

func (openCodeAssistant) ID() string     { return "opencode" }
func (openCodeAssistant) Name() string   { return "OpenCode" }
func (openCodeAssistant) Reload() string { return "Reinicia OpenCode o abre una sesión nueva." }
func (openCodeAssistant) cli(e Env) string {
	return findOpenCode(e.GOOS, e.Home, e.AppData, e.LookPath, e.Stat)
}
func (a openCodeAssistant) Detect(e Env) bool {
	return a.cli(e) != "" || dirExists(e, userpaths.OpenCode(e.Home, e.XDGConfigHome))
}

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
		if isExecutable(goos, stat, p) {
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

// path returns the configuration file. Having both opencode.json and
// opencode.jsonc is ambiguous, so it is an error.
func (a openCodeAssistant) path(e Env) (string, error) {
	dir := userpaths.OpenCode(e.Home, e.XDGConfigHome)
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

func openCodeArgs(exe string, extra []string) []string {
	args := []string{"mcp", "add", aulaglobalKey, "--global"}
	for _, pair := range approvedEnv(extra) {
		args = append(args, "--env", pair)
	}
	return append(args, "--", exe, "--transport", "stdio")
}

func openCodeEntry(exe string, extra []string) map[string]any {
	entry := map[string]any{"type": "local", "command": []string{exe, "--transport", "stdio"}}
	if env := envMap(extra); len(env) > 0 {
		entry["environment"] = env
	}
	return entry
}

func (a openCodeAssistant) read(e Env, w Entry) (string, []byte, Inspection) {
	p, old, early, done := loadConfig(e, a.Detect(e), a.path)
	if done {
		return p, old, early
	}
	if len(old) > 0 {
		if err := strictJSON(old); err != nil {
			return p, old, Inspection{Err: err}
		}
	}
	_, exists, err := openCodeConfig(old, openCodeEntry(w.Exe, w.Extra), false)
	if err != nil {
		return p, old, Inspection{Err: err}
	}
	if !exists {
		return p, old, Inspection{State: Missing}
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
	if sameEntry(m[aulaglobalKey], want) {
		return p, old, Inspection{State: Identical}
	}
	detail := "entrada existente"
	if b, ok := m[aulaglobalKey].(map[string]any); ok {
		if cmd, ok := b["command"].([]any); ok && len(cmd) > 0 {
			v, _ := json.Marshal(cmd[0])
			detail = "comando " + string(v)
		}
	}
	return p, old, Inspection{State: Different, Detail: detail}
}

func (a openCodeAssistant) Inspect(e Env, w Entry) Inspection {
	_, _, i := a.read(e, w)
	return i
}

func (a openCodeAssistant) Apply(e Env, w Entry, replace bool) error {
	p, old, i := a.read(e, w)
	if i.Err != nil {
		return i.Err
	}
	if i.State == Identical {
		return nil
	}
	if i.State == NotInstalled {
		return errors.New("not installed")
	}
	if i.State == Different && !replace {
		return errors.New("replacement not authorized")
	}
	cli := a.cli(e)
	scriptable := false
	if cli != "" {
		help, err := e.call(e.RunOpenCode, cli, "mcp", "add", "--help")
		scriptable = err == nil && strings.Contains(string(help), "--global")
	}
	b, _, err := openCodeConfig(old, openCodeEntry(w.Exe, w.Extra), replace)
	if err != nil {
		return err
	}
	if cli != "" && scriptable && i.State == Missing {
		if _, err = e.call(e.RunOpenCode, cli, openCodeArgs(w.Exe, w.Extra)...); err != nil {
			return err
		}
		// A zero exit status is not enough: check that the global entry is there.
		_, _, check := a.read(e, w)
		if check.Err != nil {
			return check.Err
		}
		if check.State != Identical {
			return errors.New("unverified global entry")
		}
		return nil
	}
	if !dirExists(e, filepath.Dir(p)) {
		return errors.New("global directory missing")
	}
	return e.write(p, b)
}

func (a openCodeAssistant) Manual(w Entry) string {
	v2, _, _ := openCodeConfig(nil, openCodeEntry(w.Exe, w.Extra), true)
	v1, _, _ := openCodeConfig([]byte(`{"mcp":{"aulaglobal":{"type":"local","command":["old"]}}}`), openCodeEntry(w.Exe, w.Extra), true)
	return "Alternativa manual · OpenCode:\nV2 actual: mcp.servers, sin enabled.\n" + string(v2) + "V1 antiguo: mcp, con enabled.\n" + string(v1)
}

// openCodeConfig sets the aulaglobal entry in opencode.json, in whichever
// format (V1 or V2) the file already uses, and keeps every other key. It only
// accepts strict JSON: a JSONC file is never rewritten, so its comments stay.
// exists reports whether the entry was already there; it is replaced only when
// replace is true.
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
	_, exists := servers[aulaglobalKey]
	if exists && !replace {
		return nil, true, nil
	}
	b, e := json.Marshal(copyEntry)
	if e != nil {
		return nil, exists, e
	}
	servers[aulaglobalKey] = b
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

// openCodeServersPath returns where the servers live in old: mcp.servers for
// V2, mcp for V1.
func openCodeServersPath(old []byte) ([]string, error) {
	var root map[string]json.RawMessage
	if e := json.Unmarshal(old, &root); e != nil || root == nil {
		return nil, errors.New("object required")
	}
	raw, ok := root["mcp"]
	if !ok {
		return []string{"mcp"}, nil
	}
	var mcp map[string]json.RawMessage
	if e := json.Unmarshal(raw, &mcp); e != nil || mcp == nil {
		return nil, errors.New("mcp must be an object")
	}
	if _, ok := mcp["servers"]; ok {
		return []string{"mcp", "servers"}, nil
	}
	return []string{"mcp"}, nil
}

func (a openCodeAssistant) entryOf(old []byte) (map[string]any, bool, error) {
	path, err := openCodeServersPath(old)
	if err != nil {
		return nil, false, err
	}
	return jsonServerEntry(old, path)
}

func (a openCodeAssistant) removeFrom(old []byte) ([]byte, bool, error) {
	path, err := openCodeServersPath(old)
	if err != nil {
		return nil, false, err
	}
	return jsonRemoveServer(old, path)
}

func (a openCodeAssistant) Find(e Env) Link {
	return findInConfig(e, a.Detect(e), a.path, a.entryOf, "no se puede interpretar con seguridad (¿JSONC o formato ambiguo?)")
}

func (a openCodeAssistant) Remove(e Env) error {
	return removeFromConfig(e, a.path, a.removeFrom)
}

func (a openCodeAssistant) ManualRemove(e Env) string {
	p, _ := a.path(e)
	if p == "" {
		p = filepath.Join("~", ".config", "opencode", "opencode.json")
	}
	return "Alternativa manual · OpenCode: abre " + p + " y borra la clave «aulaglobal» dentro de «mcp.servers» (V2) o de «mcp» (V1)."
}
