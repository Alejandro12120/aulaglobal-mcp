package assistants

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// fileAssistant is an assistant whose whole configuration is one JSON or TOML
// file: Claude Desktop, Codex and Antigravity. The differences between them are
// the fields below. Reading, merging, comparing and backing up are shared.
type fileAssistant struct {
	id, name, reload string
	path             func(Env) (string, error)
	detect           func(Env) bool
	// merge adds entry to a config and reports whether the aulaglobal entry was
	// already there. It returns no bytes when the entry exists and replace is false.
	merge    func(old []byte, entry map[string]any, replace bool) ([]byte, bool, error)
	toml     bool
	generate func(Env, Entry) (map[string]any, error)
	// entryOf and removeFrom serve uninstall.
	entryOf    func([]byte) (map[string]any, bool, error)
	removeFrom func([]byte) ([]byte, bool, error)
	// agy writes "disabled": false by default, which means the same as omitting it.
	ignoreDisabledFalse bool
}

func (a fileAssistant) ID() string        { return a.id }
func (a fileAssistant) Name() string      { return a.name }
func (a fileAssistant) Reload() string    { return a.reload }
func (a fileAssistant) Detect(e Env) bool { return a.detect(e) }

// loadConfig reads an assistant's configuration file. When the answer is already
// known (not installed, or unreadable) done is true and early holds it. A missing
// file is not an error: old is then empty.
func loadConfig(e Env, detected bool, path func(Env) (string, error)) (p string, old []byte, early Inspection, done bool) {
	if !detected {
		return "", nil, Inspection{State: NotInstalled}, true
	}
	p, err := path(e)
	if err != nil {
		return p, nil, Inspection{Err: err}, true
	}
	old, err = os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return p, nil, Inspection{Err: err}, true
	}
	return p, old, Inspection{}, false
}

func (a fileAssistant) read(e Env, w Entry) (string, []byte, Inspection) {
	p, old, early, done := loadConfig(e, a.Detect(e), a.path)
	if done {
		return p, old, early
	}
	_, exists, err := a.merge(old, w.Config(), false)
	if err != nil {
		return p, old, Inspection{Err: err}
	}
	if !exists {
		return p, old, Inspection{State: Missing}
	}
	var root map[string]any
	key := "mcpServers"
	if a.toml {
		key = "mcp_servers"
		err = toml.Unmarshal(old, &root)
	} else {
		err = json.Unmarshal(old, &root)
	}
	servers, _ := root[key].(map[string]any)
	entry := servers[aulaglobalKey]
	if sameEntry(entry, w.Config()) {
		return p, old, Inspection{State: Identical}
	}
	if a.ignoreDisabledFalse {
		if m, ok := entry.(map[string]any); ok && m["disabled"] == false {
			delete(m, "disabled")
			if sameEntry(m, w.Config()) {
				return p, old, Inspection{State: Identical}
			}
		}
	}
	detail := "entrada existente"
	if m, ok := entry.(map[string]any); ok {
		if command, ok := m["command"].(string); ok {
			detail = "comando " + fmt.Sprintf("%q", command)
		}
	}
	return p, old, Inspection{State: Different, Detail: detail, Err: err}
}

func (a fileAssistant) Inspect(e Env, w Entry) Inspection {
	_, _, i := a.read(e, w)
	return i
}

func (a fileAssistant) Apply(e Env, w Entry, replace bool) error {
	p, old, i := a.read(e, w)
	if i.Err != nil {
		return i.Err
	}
	if i.State == NotInstalled {
		return errors.New("not installed")
	}
	if i.State == Identical {
		return nil
	}
	if i.State == Different && !replace {
		return errors.New("replacement not authorized")
	}
	entry := w.Config()
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

func (a fileAssistant) Manual(w Entry) string {
	b, _, _ := a.merge(nil, w.Config(), true)
	return "Alternativa manual · " + a.name + ":\n" + string(b)
}

func (a fileAssistant) Find(e Env) Link {
	return findInConfig(e, a.Detect(e), a.path, a.entryOf, "no se puede interpretar con seguridad")
}

func (a fileAssistant) Remove(e Env) error {
	return removeFromConfig(e, a.path, a.removeFrom)
}

func (a fileAssistant) ManualRemove(e Env) string {
	p, _ := a.path(e)
	if p == "" {
		p = a.name
	}
	if a.toml {
		return "Alternativa manual · " + a.name + ": abre " + p + " y borra la tabla [mcp_servers.aulaglobal] (y [mcp_servers.aulaglobal.env] si existe)."
	}
	return "Alternativa manual · " + a.name + ": abre " + p + " y borra la clave «aulaglobal» dentro de «mcpServers»."
}

// findInConfig is Find for an assistant with one configuration file. entryOf
// returns the aulaglobal entry in the file's bytes, and unsafeDetail explains
// why a file it cannot interpret is left alone.
func findInConfig(e Env, detected bool, path func(Env) (string, error), entryOf func([]byte) (map[string]any, bool, error), unsafeDetail string) Link {
	if !detected {
		return Link{State: LinkNotInstalled}
	}
	p, err := path(e)
	if err != nil {
		return Link{State: LinkError, Detail: err.Error()}
	}
	old, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return Link{State: LinkNone, Location: p}
	}
	if err != nil {
		return Link{State: LinkError, Location: p, Detail: err.Error()}
	}
	entry, ok, err := entryOf(old)
	if err != nil {
		return Link{State: LinkError, Location: p, Detail: unsafeDetail}
	}
	if !ok {
		return Link{State: LinkNone, Location: p}
	}
	l := linkFromEntry(entry)
	l.Location = p
	return l
}

// removeFromConfig is Remove for an assistant with one configuration file.
// removeFrom drops the aulaglobal entry from the file's bytes and reports
// whether it was there; a file without the entry is left untouched.
func removeFromConfig(e Env, path func(Env) (string, error), removeFrom func([]byte) ([]byte, bool, error)) error {
	p, err := path(e)
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
	b, found, err := removeFrom(old)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return e.write(p, b)
}
