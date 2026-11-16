package main

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
	path             func(clientEnv) (string, error)
	detect           func(clientEnv) bool
	// merge adds entry to a config and reports whether the aulaglobal entry was
	// already there. It returns no bytes when the entry exists and replace is false.
	merge    func(old []byte, entry map[string]any, replace bool) ([]byte, bool, error)
	toml     bool
	generate func(clientEnv, desiredEntry) (map[string]any, error)
	// entryOf and removeFrom serve uninstall.
	entryOf    func([]byte) (map[string]any, bool, error)
	removeFrom func([]byte) ([]byte, bool, error)
	// agy writes "disabled": false by default, which means the same as omitting it.
	ignoreDisabledFalse bool
}

func (a fileAssistant) ID() string              { return a.id }
func (a fileAssistant) Name() string            { return a.name }
func (a fileAssistant) Reload() string          { return a.reload }
func (a fileAssistant) Detect(e clientEnv) bool { return a.detect(e) }

// loadConfig reads an assistant's configuration file. When the answer is already
// known (not installed, or unreadable) done is true and early holds it. A missing
// file is not an error: old is then empty.
func loadConfig(e clientEnv, detected bool, path func(clientEnv) (string, error)) (p string, old []byte, early inspection, done bool) {
	if !detected {
		return "", nil, inspection{state: notInstalledHere}, true
	}
	p, err := path(e)
	if err != nil {
		return p, nil, inspection{err: err}, true
	}
	old, err = os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return p, nil, inspection{err: err}, true
	}
	return p, old, inspection{}, false
}

func (a fileAssistant) read(e clientEnv, w desiredEntry) (string, []byte, inspection) {
	p, old, early, done := loadConfig(e, a.Detect(e), a.path)
	if done {
		return p, old, early
	}
	_, exists, err := a.merge(old, w.entry(), false)
	if err != nil {
		return p, old, inspection{err: err}
	}
	if !exists {
		return p, old, inspection{state: missing}
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
	if sameEntry(entry, w.entry()) {
		return p, old, inspection{state: identical}
	}
	if a.ignoreDisabledFalse {
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

func (a fileAssistant) Find(e clientEnv) link {
	return findInConfig(e, a.Detect(e), a.path, a.entryOf, "no se puede interpretar con seguridad")
}

func (a fileAssistant) Remove(e clientEnv) error {
	return removeFromConfig(e, a.path, a.removeFrom)
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

// findInConfig is Find for an assistant with one configuration file. entryOf
// returns the aulaglobal entry in the file's bytes, and unsafeDetail explains
// why a file it cannot interpret is left alone.
func findInConfig(e clientEnv, detected bool, path func(clientEnv) (string, error), entryOf func([]byte) (map[string]any, bool, error), unsafeDetail string) link {
	if !detected {
		return link{state: linkNotInstalled}
	}
	p, err := path(e)
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
	entry, ok, err := entryOf(old)
	if err != nil {
		return link{state: linkError, location: p, detail: unsafeDetail}
	}
	if !ok {
		return link{state: linkNone, location: p}
	}
	l := linkFromEntry(entry)
	l.location = p
	return l
}

// removeFromConfig is Remove for an assistant with one configuration file.
// removeFrom drops the aulaglobal entry from the file's bytes and reports
// whether it was there; a file without the entry is left untouched.
func removeFromConfig(e clientEnv, path func(clientEnv) (string, error), removeFrom func([]byte) ([]byte, bool, error)) error {
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
