package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// linkState says what an assistant's configuration holds for "aulaglobal".
type linkState int

const (
	linkNotInstalled linkState = iota // assistant not found on this computer
	linkNone                          // installed, no aulaglobal entry
	linkPresent                       // an entry we can remove
	linkElsewhere                     // Claude Code local/project scope: explained, never touched
	linkError                         // cannot be read or modified safely
)

// link is what Find reports about one assistant. command and env come from the
// entry itself so the caller can tell whether it points at this program.
type link struct {
	state    linkState
	command  string
	env      map[string]string
	detail   string // explanation for linkElsewhere / linkError
	location string // file or command shown in the plan
}

const aulaglobalKey = "aulaglobal"

// linkFromEntry reads command and env from any client's entry shape: JSON
// "command" string or OpenCode "command" array, "env" or "environment".
func linkFromEntry(m map[string]any) link {
	l := link{state: linkPresent, env: map[string]string{}}
	switch c := m["command"].(type) {
	case string:
		l.command = c
	case []any:
		if len(c) > 0 {
			l.command, _ = c[0].(string)
		}
	}
	for _, k := range []string{"env", "environment"} {
		if vars, ok := m[k].(map[string]any); ok {
			for name, v := range vars {
				if s, ok := v.(string); ok {
					l.env[name] = s
				}
			}
		}
	}
	return l
}

// ---- JSON: byte-exact member removal -------------------------------------

func jsonSkipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// jsonMemberSpan finds member key inside the object at path and returns the
// byte range whose removal leaves valid JSON with every other byte untouched
// (same key order, indentation and number formatting). The document must have
// passed strictAntigravityJSON, so keys are unique.
func jsonMemberSpan(data []byte, path []string, key string) (start, end int, found bool, err error) {
	d := json.NewDecoder(bytes.NewReader(data))
	if tok, e := d.Token(); e != nil || tok != json.Delim('{') {
		return 0, 0, false, errors.New("object required")
	}
	for len(path) > 0 {
		match := false
		for d.More() {
			kt, e := d.Token()
			if e != nil {
				return 0, 0, false, e
			}
			if kt.(string) == path[0] {
				vt, e := d.Token()
				if e != nil {
					return 0, 0, false, e
				}
				if vt != json.Delim('{') {
					return 0, 0, false, fmt.Errorf("%s must be an object", path[0])
				}
				match = true
				break
			}
			var skip json.RawMessage
			if e = d.Decode(&skip); e != nil {
				return 0, 0, false, e
			}
		}
		if !match {
			return 0, 0, false, nil
		}
		path = path[1:]
	}
	prevEnd := int(d.InputOffset())
	first := true
	for d.More() {
		kt, e := d.Token()
		if e != nil {
			return 0, 0, false, e
		}
		var raw json.RawMessage
		if e = d.Decode(&raw); e != nil {
			return 0, 0, false, e
		}
		valEnd := int(d.InputOffset())
		if kt.(string) != key {
			prevEnd, first = valEnd, false
			continue
		}
		switch {
		case !first:
			return prevEnd, valEnd, true, nil
		default:
			i := jsonSkipSpace(data, valEnd)
			if i < len(data) && data[i] == ',' {
				return jsonSkipSpace(data, prevEnd), jsonSkipSpace(data, i+1), true, nil
			}
			return prevEnd, i, true, nil // only member: leave "{}"
		}
	}
	return 0, 0, false, nil
}

// jsonRemoveServer removes servers[path...].aulaglobal. found=false means the
// entry was not there and nothing needs writing.
func jsonRemoveServer(old []byte, path []string) ([]byte, bool, error) {
	if e := strictAntigravityJSON(old); e != nil {
		return nil, false, e
	}
	s, e, found, err := jsonMemberSpan(old, path, aulaglobalKey)
	if err != nil || !found {
		return nil, false, err
	}
	out := append(append([]byte{}, old[:s]...), old[e:]...)
	if !json.Valid(out) {
		return nil, true, errors.New("unsafe removal")
	}
	if _, _, again, err := jsonMemberSpan(out, path, aulaglobalKey); err != nil || again {
		return nil, true, errors.New("entry still present")
	}
	return out, true, nil
}

// jsonServerEntry returns the aulaglobal object under path, if any.
func jsonServerEntry(old []byte, path []string) (map[string]any, bool, error) {
	if len(bytes.TrimSpace(old)) == 0 {
		return nil, false, nil
	}
	if e := strictAntigravityJSON(old); e != nil {
		return nil, false, e
	}
	var cur any
	if e := json.Unmarshal(old, &cur); e != nil {
		return nil, false, e
	}
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false, errors.New("object required")
		}
		if cur, ok = m[p]; !ok {
			return nil, false, nil
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, false, errors.New("object required")
	}
	v, ok := m[aulaglobalKey]
	if !ok {
		return nil, false, nil
	}
	entry, ok := v.(map[string]any)
	if !ok {
		return nil, true, errors.New("unexpected entry")
	}
	return entry, true, nil
}

func desktopEntry(old []byte) (map[string]any, bool, error) {
	return jsonServerEntry(old, []string{"mcpServers"})
}
func desktopRemove(old []byte) ([]byte, bool, error) {
	return jsonRemoveServer(old, []string{"mcpServers"})
}

// ---- TOML (Codex) ----------------------------------------------------------

func codexEntry(old []byte) (map[string]any, bool, error) {
	var root map[string]any
	if e := decodeTOML(old, &root); e != nil {
		return nil, false, e
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	v, ok := servers[aulaglobalKey]
	if !ok {
		return nil, false, nil
	}
	entry, ok := v.(map[string]any)
	if !ok {
		return nil, true, errors.New("unexpected entry")
	}
	return entry, true, nil
}

// codexRemove drops the [mcp_servers.aulaglobal] table and its sub-tables line
// by line, keeping every other byte. Layouts it cannot cut cleanly (inline
// tables, dotted keys, multi-line strings) are refused, never guessed.
func codexRemove(old []byte) ([]byte, bool, error) {
	_, exists, err := codexEntry(old)
	if err != nil || !exists {
		return nil, false, err
	}
	if strings.Contains(string(old), `"""`) || strings.Contains(string(old), "'''") {
		return nil, true, errors.New("multiline TOML")
	}
	var lines []string
	skipping := false
	sc := bufio.NewScanner(bytes.NewReader(old))
	sc.Buffer(make([]byte, 4096), 4<<20)
	removedAtEnd := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			var header map[string]any
			if e := decodeTOML([]byte(line+"\n"), &header); e != nil {
				return nil, true, e
			}
			servers, _ := header["mcp_servers"].(map[string]any)
			_, skipping = servers[aulaglobalKey]
			removedAtEnd = skipping
		}
		if !skipping {
			lines = append(lines, line)
		}
	}
	if e := sc.Err(); e != nil {
		return nil, true, e
	}
	if removedAtEnd { // the blank line setup put before our block goes with it
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
	}
	out := strings.Join(lines, "\n")
	if len(lines) > 0 {
		out += "\n"
	}
	if _, still, e := codexEntry([]byte(out)); e != nil || still {
		return nil, true, errors.New("unsupported TOML layout")
	}
	return []byte(out), true, nil
}

// ---- file-based assistants -------------------------------------------------

func (a fileAssistant) Find(e clientEnv) link {
	if !a.Detect(e) {
		return link{state: linkNotInstalled}
	}
	p, err := a.path(e)
	if err != nil {
		return link{state: linkError, detail: err.Error()}
	}
	old, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return link{state: linkNone, location: p}
	}
	if err != nil {
		return link{state: linkError, location: p, detail: err.Error()}
	}
	entry, ok, err := a.entryOf(old)
	if err != nil {
		return link{state: linkError, location: p, detail: "no se puede interpretar con seguridad"}
	}
	if !ok {
		return link{state: linkNone, location: p}
	}
	l := linkFromEntry(entry)
	l.location = p
	return l
}

func (a fileAssistant) Remove(e clientEnv) error {
	p, err := a.path(e)
	if err != nil {
		return err
	}
	old, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	b, found, err := a.removeFrom(old)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return e.write(p, b)
}

func (a fileAssistant) ManualRemove(e clientEnv) string {
	p, _ := a.path(e)
	if p == "" {
		p = a.name
	}
	if a.toml {
		return "Alternativa manual · " + a.name + ": abre " + p + " y borra la tabla [mcp_servers.aulaglobal] (y [mcp_servers.aulaglobal.env] si existe)."
	}
	return "Alternativa manual · " + a.name + ": abre " + p + " y borra la clave «aulaglobal» dentro de «mcpServers»."
}

// ---- OpenCode ------------------------------------------------------------

// openCodePath returns where servers live: V2 mcp.servers, else V1 mcp.
func openCodePath(old []byte) ([]string, error) {
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

func (a openCodeAssistant) Find(e clientEnv) link {
	if !a.Detect(e) {
		return link{state: linkNotInstalled}
	}
	p, err := a.path(e)
	if err != nil {
		return link{state: linkError, detail: err.Error()}
	}
	old, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return link{state: linkNone, location: p}
	}
	if err != nil {
		return link{state: linkError, location: p, detail: err.Error()}
	}
	path, err := openCodePath(old)
	if err == nil {
		var entry map[string]any
		var ok bool
		if entry, ok, err = jsonServerEntry(old, path); err == nil {
			if !ok {
				return link{state: linkNone, location: p}
			}
			l := linkFromEntry(entry)
			l.location = p
			return l
		}
	}
	return link{state: linkError, location: p, detail: "no se puede interpretar con seguridad (¿JSONC o formato ambiguo?)"}
}

func (a openCodeAssistant) Remove(e clientEnv) error {
	p, err := a.path(e)
	if err != nil {
		return err
	}
	old, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	path, err := openCodePath(old)
	if err != nil {
		return err
	}
	b, found, err := jsonRemoveServer(old, path)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return e.write(p, b)
}

func (a openCodeAssistant) ManualRemove(e clientEnv) string {
	p, _ := a.path(e)
	if p == "" {
		p = filepath.Join("~", ".config", "opencode", "opencode.json")
	}
	return "Alternativa manual · OpenCode: abre " + p + " y borra la clave «aulaglobal» dentro de «mcp.servers» (V2) o de «mcp» (V1)."
}

// ---- Claude Code -----------------------------------------------------------

func (a claudeAssistant) Find(e clientEnv) link {
	cli := a.cli(e)
	if cli == "" {
		return link{state: linkNotInstalled}
	}
	b, err := e.call(e.Run, cli, "mcp", "get", aulaglobalKey)
	if err != nil {
		lower := strings.ToLower(string(b))
		if strings.Contains(lower, "no mcp server named") || strings.Contains(lower, "no mcp server found") {
			return link{state: linkNone}
		}
		return link{state: linkError, detail: "no se puede consultar a Claude Code"}
	}
	lines, env, scope := parseClaudeGet(string(b))
	switch scope {
	case "user":
		return link{state: linkPresent, command: lines["Command"], env: env, detail: "claude mcp remove --scope user aulaglobal"}
	case "local":
		return link{state: linkElsewhere, command: lines["Command"], detail: "está configurado solo para un proyecto (ámbito local). No se toca; para quitarlo, ejecuta en esa carpeta: claude mcp remove --scope local aulaglobal"}
	case "project":
		return link{state: linkElsewhere, command: lines["Command"], detail: "lo define un proyecto en su .mcp.json. No se toca ese archivo; si no lo quieres, bórralo tú de allí."}
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
