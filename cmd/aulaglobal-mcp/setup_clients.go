package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type clientState int

const (
	notInstalledHere clientState = iota
	missing
	identical
	different
	otherScope
)

type inspection struct {
	state         clientState
	detail, scope string
	err           error
}
type desiredEntry struct {
	Exe   string
	Extra []string
}

func (w desiredEntry) entry() map[string]any {
	e := map[string]any{"command": w.Exe, "args": []string{"--transport", "stdio"}}
	if env := openCodeEntry(w.Exe, w.Extra)["environment"]; env != nil {
		e["env"] = env
	}
	return e
}
func desired(exe, token, data, defaultToken, defaultData string) (desiredEntry, error) {
	p, e := filepath.Abs(exe)
	if e != nil {
		return desiredEntry{}, e
	}
	w := desiredEntry{Exe: p}
	for _, v := range []struct{ k, v, d string }{{"AULAGLOBAL_TOKEN_FILE", token, defaultToken}, {"AULAGLOBAL_DOWNLOAD_DIR", data, defaultData}} {
		if v.v != v.d {
			w.Extra = append(w.Extra, "-e", v.k+"="+v.v)
		}
	}
	return w, nil
}

type clientEnv struct {
	Write                                          func(string, []byte) error
	GOOS, Home, Config, AppData, LocalAppData, XDG string
	Look                                           func(string) (string, error)
	Stat                                           func(string) (os.FileInfo, error)
	Run                                            claudeRunner
	OpenRun                                        claudeRunner
	GenerateAG                                     func(string, string, []string) (map[string]any, error)
}

func (e clientEnv) call(run claudeRunner, cli string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return run(ctx, cli, args...)
}

type assistant interface {
	ID() string
	Name() string
	Detect(clientEnv) bool
	Inspect(clientEnv, desiredEntry) inspection
	Apply(clientEnv, desiredEntry, bool) error
	Manual(desiredEntry) string
	Reload() string
	// Find, Remove and ManualRemove serve `uninstall`: they look for the entry
	// named aulaglobal and remove only that entry, never anything else.
	Find(clientEnv) link
	Remove(clientEnv) error
	ManualRemove(clientEnv) string
}

// Ordered once, used by selection, help and contract tests.
var assistants = []assistant{claudeAssistant{}, desktopAssistant(), codexAssistant(), openCodeAssistant{}, antigravityAssistant()}

func selectAssistants(ids string) ([]assistant, error) {
	if ids == "" {
		return assistants, nil
	}
	selected := map[string]bool{}
	for _, id := range strings.Split(ids, ",") {
		selected[strings.TrimSpace(id)] = true
	}
	var out []assistant
	for _, a := range assistants {
		if selected[a.ID()] {
			out = append(out, a)
			delete(selected, a.ID())
		}
	}
	if len(selected) > 0 {
		return nil, errors.New("Cliente desconocido; usa --help para consultar los IDs válidos")
	}
	return out, nil
}
func clientIDs() string {
	var ids []string
	for _, a := range assistants {
		ids = append(ids, a.ID())
	}
	return strings.Join(ids, ",")
}
func confirmClient(u setupUI, q string, yes bool) bool {
	if yes {
		return true
	}
	fmt.Fprint(u.out, "› "+q+" [S/n] ")
	a := strings.ToLower(strings.TrimSpace(u.line()))
	fmt.Fprintln(u.out)
	return a == "" || a == "s" || a == "si" || a == "sí"
}
func configureClients(u setupUI, e clientEnv, w desiredEntry, list []assistant, yes bool) bool {
	type found struct {
		a       assistant
		i       inspection
		outcome string
		changed bool
	}
	var clients []*found
	var absent []string
	changes := 0
	fmt.Fprintln(u.out, "Asistentes encontrados en este ordenador:")
	for _, a := range list {
		if !a.Detect(e) {
			absent = append(absent, a.Name())
			continue
		}
		i := a.Inspect(e, w)
		clients = append(clients, &found{a: a, i: i})
		fmt.Fprintln(u.out, "  ✓ "+a.Name())
		if i.state != identical && i.err == nil {
			changes++
		}
	}
	if len(clients) == 0 {
		fmt.Fprintln(u.out, "  Ninguno.")
	}
	if len(absent) > 0 {
		fmt.Fprintln(u.out, "No encontrados: "+strings.Join(absent, ", "))
	}
	all := yes
	if changes >= 2 && !yes {
		fmt.Fprint(u.out, "\n› ¿Configurar todos? [S/n/e=elegir uno a uno] ")
		answer := strings.ToLower(strings.TrimSpace(u.line()))
		fmt.Fprintln(u.out)
		all = answer == "" || answer == "s" || answer == "si" || answer == "sí"
		if answer == "n" {
			for _, c := range clients {
				if c.i.state != identical && c.i.err == nil {
					c.outcome = "omitido"
				}
			}
		}
	}
	failed := false
	for _, c := range clients {
		fmt.Fprintln(u.out)
		a, i := c.a, c.i
		fail := func() {
			failed = true
			c.outcome = "falló → ver instrucciones arriba"
			fmt.Fprintln(u.out, "✗ "+a.Name()+": no se pudo configurar con seguridad.")
			fmt.Fprintln(u.out, a.Manual(w))
		}
		if i.err != nil {
			fail()
			continue
		}
		if i.state == identical {
			c.outcome = "ya estaba"
			fmt.Fprintln(u.out, "✓ "+a.Name()+": ya está configurado.")
			continue
		}
		q := "¿Añadir AulaGlobal a " + a.Name() + "?"
		if i.state == different {
			fmt.Fprintln(u.out, "! "+a.Name()+": configuración diferente ("+i.detail+").")
			fmt.Fprintln(u.out, "  Se usará: "+w.Exe+" --transport stdio")
			q = "¿Actualizarlo?"
		} else if i.state == otherScope {
			fmt.Fprintln(u.out, "! "+a.Name()+": "+i.detail)
			if i.scope == "local" {
				q = "¿Mover AulaGlobal al ámbito de usuario?"
			} else {
				q = "¿Añadir también AulaGlobal al ámbito de usuario?"
			}
		}
		if c.outcome == "omitido" || !confirmClient(u, q, all) {
			c.outcome = "omitido"
			fmt.Fprintln(u.out, "· "+a.Name()+": omitido.")
			continue
		}
		if err := a.Apply(e, w, i.state != missing); err != nil {
			fail()
			continue
		}
		c.changed = true
		c.outcome = "añadido"
		if i.state != missing {
			c.outcome = "actualizado"
		}
		fmt.Fprintln(u.out, "✓ "+a.Name()+": "+c.outcome+".")
	}
	fmt.Fprintln(u.out, "\nResumen:")
	for _, c := range clients {
		fmt.Fprintln(u.out, "  "+c.a.Name()+": "+c.outcome)
	}
	changed := false
	for _, c := range clients {
		if c.changed {
			if !changed {
				fmt.Fprintln(u.out)
			}
			changed = true
			fmt.Fprintln(u.out, c.a.Name()+": "+c.a.Reload())
		}
	}
	if changed {
		fmt.Fprintln(u.out, "\nPrueba a pedir: «¿Qué asignaturas tengo?»")
	} else if !failed {
		fmt.Fprintln(u.out, "\nNo se ha cambiado ninguna configuración.")
	}
	return failed
}
func (e clientEnv) write(path string, b []byte) error {
	if e.Write != nil {
		return e.Write(path, b)
	}
	return atomicConfig(path, b)
}

func realClientEnv(goos, home, config string) clientEnv {
	return clientEnv{GOOS: goos, Home: home, Config: config, AppData: os.Getenv("APPDATA"), LocalAppData: os.Getenv("LOCALAPPDATA"), XDG: os.Getenv("XDG_CONFIG_HOME"), Look: exec.LookPath, Stat: os.Stat, Run: runClaude, OpenRun: runOpenCode, GenerateAG: antigravityCLIEntry}
}

// File clients share inspection, safe merge, equality and atomic backups.
type fileAssistant struct {
	id, name, reload string
	path             func(clientEnv) (string, error)
	detect           func(clientEnv) bool
	merge            func([]byte, map[string]any, bool) ([]byte, bool, error)
	toml             bool
	generate         func(clientEnv, desiredEntry) (map[string]any, error)
	entryOf          func([]byte) (map[string]any, bool, error)
	removeFrom       func([]byte) ([]byte, bool, error)
}

func (a fileAssistant) ID() string              { return a.id }
func (a fileAssistant) Name() string            { return a.name }
func (a fileAssistant) Reload() string          { return a.reload }
func (a fileAssistant) Detect(e clientEnv) bool { return a.detect(e) }
func sameEntry(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func (a fileAssistant) read(e clientEnv, w desiredEntry) (string, []byte, inspection) {
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
	_, exists, err := a.merge(old, w.entry(), false)
	if err != nil {
		return p, old, inspection{err: err}
	}
	if !exists {
		return p, old, inspection{state: missing}
	}
	var root map[string]any
	if a.toml {
		err = decodeTOML(old, &root)
	} else {
		err = json.Unmarshal(old, &root)
	}
	key := "mcpServers"
	if a.toml {
		key = "mcp_servers"
	}
	servers, _ := root[key].(map[string]any)
	entry := servers["aulaglobal"]
	if sameEntry(entry, w.entry()) {
		return p, old, inspection{state: identical}
	}
	// agy emits disabled:false by default; this is semantically identical.
	if a.id == "antigravity" {
		if m, ok := entry.(map[string]any); ok && m["disabled"] == false {
			delete(m, "disabled")
			if sameEntry(m, w.entry()) {
				return p, old, inspection{state: identical}
			}
		}
	}
	detail := "entrada existente"
	if m, ok := entry.(map[string]any); ok {
		if command, ok := m["command"].(string); ok {
			detail = "comando " + fmt.Sprintf("%q", command)
		}
	}
	return p, old, inspection{state: different, detail: detail, err: err}
}
func (a fileAssistant) Inspect(e clientEnv, w desiredEntry) inspection {
	_, _, i := a.read(e, w)
	return i
}
func (a fileAssistant) Apply(e clientEnv, w desiredEntry, replace bool) error {
	p, old, i := a.read(e, w)
	if i.err != nil {
		return i.err
	}
	if i.state == notInstalledHere {
		return errors.New("not installed")
	}
	if i.state == identical {
		return nil
	}
	if i.state == different && !replace {
		return errors.New("replacement not authorized")
	}
	entry := w.entry()
	if a.generate != nil {
		var err error
		entry, err = a.generate(e, w)
		if err != nil {
			return err
		}
	}
	b, _, err := a.merge(old, entry, replace)
	if err != nil {
		return err
	}
	return e.write(p, b)
}
func (a fileAssistant) Manual(w desiredEntry) string {
	b, _, _ := a.merge(nil, w.entry(), true)
	return "Alternativa manual · " + a.name + ":\n" + string(b)
}
func dirExists(e clientEnv, p string) bool { f, err := e.Stat(p); return err == nil && f.IsDir() }
