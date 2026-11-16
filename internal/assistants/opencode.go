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
func (openCodeAssistant) cli(env Env) string {
	return findOpenCode(env.GOOS, env.Home, env.AppData, env.LookPath, env.Stat)
}
func (a openCodeAssistant) Detect(env Env) bool {
	return a.cli(env) != "" || dirExists(env, userpaths.OpenCode(env.Home, env.XDGConfigHome))
}

func findOpenCode(goos, home, appData string, lookPath func(string) (string, error), stat func(string) (os.FileInfo, error)) string {
	names := []string{"opencode"}
	if goos == "windows" {
		names = []string{"opencode.exe", "opencode.cmd"}
	}
	for _, name := range names {
		if found, err := lookPath(name); err == nil {
			return found
		}
	}
	var candidates []string
	for _, name := range names {
		for _, dir := range []string{filepath.Join(home, ".opencode", "bin"), filepath.Join(home, ".local", "bin"), filepath.Join(home, ".npm-global", "bin"), filepath.Join(home, ".npm", "bin")} {
			candidates = append(candidates, filepath.Join(dir, name))
		}
		if goos == "windows" && appData != "" {
			candidates = append(candidates, filepath.Join(appData, "npm", name))
		}
	}
	if goos != "windows" {
		candidates = append(candidates, "/opt/homebrew/bin/opencode", "/usr/local/bin/opencode", "/usr/bin/opencode")
	}
	for _, candidate := range candidates {
		if isExecutable(goos, stat, candidate) {
			return candidate
		}
	}
	return ""
}

func runOpenCode(ctx context.Context, cliPath string, args ...string) ([]byte, error) {
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(cliPath), ".cmd") {
		script := filepath.Join(filepath.Dir(cliPath), "node_modules", "opencode-ai", "bin", "opencode")
		if _, err := os.Stat(script); err != nil {
			return nil, err
		}
		node, err := exec.LookPath("node")
		if err != nil {
			return nil, err
		}
		return exec.CommandContext(ctx, node, append([]string{script}, args...)...).CombinedOutput()
	}
	return exec.CommandContext(ctx, cliPath, args...).CombinedOutput()
}

// path returns the configuration file. Having both opencode.json and
// opencode.jsonc is ambiguous, so it is an error.
func (a openCodeAssistant) path(env Env) (string, error) {
	dir := userpaths.OpenCode(env.Home, env.XDGConfigHome)
	jsonPath := filepath.Join(dir, "opencode.json")
	jsoncPath := filepath.Join(dir, "opencode.jsonc")
	_, jsoncErr := env.Stat(jsoncPath)
	_, jsonErr := env.Stat(jsonPath)
	if jsoncErr == nil {
		if jsonErr == nil {
			return "", errors.New("conflicting files")
		}
		if !os.IsNotExist(jsonErr) {
			return "", jsonErr
		}
		return jsoncPath, nil
	}
	if !os.IsNotExist(jsoncErr) {
		return "", jsoncErr
	}
	return jsonPath, nil
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

func (a openCodeAssistant) read(env Env, want Entry) (string, []byte, Inspection) {
	path, content, known := loadConfig(env, a.Detect(env), a.path)
	if known != nil {
		return path, content, *known
	}
	if len(content) > 0 {
		if err := strictJSON(content); err != nil {
			return path, content, Inspection{Err: err}
		}
	}
	_, exists, err := openCodeConfig(content, openCodeEntry(want.Exe, want.Extra), false)
	if err != nil {
		return path, content, Inspection{Err: err}
	}
	if !exists {
		return path, content, Inspection{State: Missing}
	}
	var root map[string]any
	json.Unmarshal(content, &root)
	servers, _ := root["mcp"].(map[string]any)
	isV1 := true
	if v2Servers, ok := servers["servers"].(map[string]any); ok {
		servers = v2Servers
		isV1 = false
	}
	wanted := openCodeEntry(want.Exe, want.Extra)
	if isV1 {
		wanted["enabled"] = true
	}
	if sameEntry(servers[aulaglobalKey], wanted) {
		return path, content, Inspection{State: Identical}
	}
	detail := "entrada existente"
	if current, ok := servers[aulaglobalKey].(map[string]any); ok {
		if command, ok := current["command"].([]any); ok && len(command) > 0 {
			program, _ := json.Marshal(command[0])
			detail = "comando " + string(program)
		}
	}
	return path, content, Inspection{State: Different, Detail: detail}
}

func (a openCodeAssistant) Inspect(env Env, want Entry) Inspection {
	_, _, inspection := a.read(env, want)
	return inspection
}

func (a openCodeAssistant) Apply(env Env, want Entry, replace bool) error {
	path, content, inspection := a.read(env, want)
	if inspection.Err != nil {
		return inspection.Err
	}
	if inspection.State == Identical {
		return nil
	}
	if inspection.State == NotInstalled {
		return errors.New("not installed")
	}
	if inspection.State == Different && !replace {
		return errors.New("replacement not authorized")
	}
	cli := a.cli(env)
	cliCanAddGlobal := false
	if cli != "" {
		help, err := env.call(env.RunOpenCode, cli, "mcp", "add", "--help")
		cliCanAddGlobal = err == nil && strings.Contains(string(help), "--global")
	}
	merged, _, err := openCodeConfig(content, openCodeEntry(want.Exe, want.Extra), replace)
	if err != nil {
		return err
	}
	if cli != "" && cliCanAddGlobal && inspection.State == Missing {
		if _, err = env.call(env.RunOpenCode, cli, openCodeArgs(want.Exe, want.Extra)...); err != nil {
			return err
		}
		// A zero exit status is not enough: check that the global entry is there.
		_, _, check := a.read(env, want)
		if check.Err != nil {
			return check.Err
		}
		if check.State != Identical {
			return errors.New("unverified global entry")
		}
		return nil
	}
	if !dirExists(env, filepath.Dir(path)) {
		return errors.New("global directory missing")
	}
	return env.write(path, merged)
}

func (a openCodeAssistant) Manual(want Entry) string {
	v2, _, _ := openCodeConfig(nil, openCodeEntry(want.Exe, want.Extra), true)
	v1, _, _ := openCodeConfig([]byte(`{"mcp":{"aulaglobal":{"type":"local","command":["old"]}}}`), openCodeEntry(want.Exe, want.Extra), true)
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
		if err := json.Unmarshal(old, &root); err != nil {
			return nil, false, err
		}
		if root == nil {
			return nil, false, errors.New("object required")
		}
	}
	servers := map[string]json.RawMessage{}
	if rawMCP, ok := root["mcp"]; ok {
		if err := json.Unmarshal(rawMCP, &servers); err != nil {
			return nil, false, err
		}
		if servers == nil {
			return nil, false, errors.New("object required")
		}
	}
	mcp := servers
	isV1 := false
	if rawServers, ok := mcp["servers"]; ok {
		servers = map[string]json.RawMessage{}
		if err := json.Unmarshal(rawServers, &servers); err != nil || servers == nil {
			return nil, false, errors.New("invalid servers")
		}
		for name, raw := range mcp {
			if name != "servers" {
				var server map[string]any
				if json.Unmarshal(raw, &server) == nil && server["type"] != nil {
					return nil, false, errors.New("mixed versions")
				}
			}
		}
	} else {
		for _, raw := range mcp {
			var server map[string]any
			if json.Unmarshal(raw, &server) == nil && server["type"] != nil {
				isV1 = true
			}
		}
		if !isV1 && len(mcp) > 0 {
			return nil, false, errors.New("ambiguous mcp")
		}
		if !isV1 {
			servers = map[string]json.RawMessage{}
		}
	}
	entryCopy := map[string]any{}
	for key, value := range entry {
		entryCopy[key] = value
	}
	if isV1 {
		entryCopy["enabled"] = true
	} else {
		delete(entryCopy, "enabled")
	}
	_, exists := servers[aulaglobalKey]
	if exists && !replace {
		return nil, true, nil
	}
	entryJSON, err := json.Marshal(entryCopy)
	if err != nil {
		return nil, exists, err
	}
	servers[aulaglobalKey] = entryJSON
	if !isV1 {
		mcp["servers"], err = json.Marshal(servers)
	} else {
		mcp = servers
	}
	root["mcp"], err = json.Marshal(mcp)
	if err != nil {
		return nil, exists, err
	}
	if len(root) == 1 {
		root["$schema"] = json.RawMessage(`"https://opencode.ai/config.json"`)
	}
	out, err := json.MarshalIndent(root, "", "  ")
	return append(out, '\n'), exists, err
}

// openCodeServersPath returns where the servers live in content: mcp.servers for
// V2, mcp for V1.
func openCodeServersPath(content []byte) ([]string, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(content, &root); err != nil || root == nil {
		return nil, errors.New("object required")
	}
	rawMCP, ok := root["mcp"]
	if !ok {
		return []string{"mcp"}, nil
	}
	var mcp map[string]json.RawMessage
	if err := json.Unmarshal(rawMCP, &mcp); err != nil || mcp == nil {
		return nil, errors.New("mcp must be an object")
	}
	if _, ok := mcp["servers"]; ok {
		return []string{"mcp", "servers"}, nil
	}
	return []string{"mcp"}, nil
}

func (a openCodeAssistant) entryOf(content []byte) (map[string]any, bool, error) {
	path, err := openCodeServersPath(content)
	if err != nil {
		return nil, false, err
	}
	return jsonServerEntry(content, path)
}

func (a openCodeAssistant) removeFrom(content []byte) ([]byte, bool, error) {
	path, err := openCodeServersPath(content)
	if err != nil {
		return nil, false, err
	}
	return jsonRemoveServer(content, path)
}

func (a openCodeAssistant) Find(env Env) Link {
	return findInConfig(env, a.Detect(env), a.path, a.entryOf, "no se puede interpretar con seguridad (¿JSONC o formato ambiguo?)")
}

func (a openCodeAssistant) Remove(env Env) error {
	return removeFromConfig(env, a.path, a.removeFrom)
}

func (a openCodeAssistant) ManualRemove(env Env) string {
	path, _ := a.path(env)
	if path == "" {
		path = filepath.Join("~", ".config", "opencode", "opencode.json")
	}
	return "Alternativa manual · OpenCode: abre " + path + " y borra la clave «aulaglobal» dentro de «mcp.servers» (V2) o de «mcp» (V1)."
}
