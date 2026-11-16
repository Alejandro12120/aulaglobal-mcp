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

func (a fileAssistant) ID() string          { return a.id }
func (a fileAssistant) Name() string        { return a.name }
func (a fileAssistant) Reload() string      { return a.reload }
func (a fileAssistant) Detect(env Env) bool { return a.detect(env) }

// loadConfig reads an assistant's configuration file. When the answer is already
// known (not installed, or unreadable) it returns it as known. A missing file
// is not an error: the content is then empty.
func loadConfig(env Env, installed bool, configPath func(Env) (string, error)) (path string, content []byte, known *Inspection) {
	if !installed {
		return "", nil, &Inspection{State: NotInstalled}
	}
	path, err := configPath(env)
	if err != nil {
		return path, nil, &Inspection{Err: err}
	}
	content, err = os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return path, nil, &Inspection{Err: err}
	}
	return path, content, nil
}

func (a fileAssistant) read(env Env, want Entry) (string, []byte, Inspection) {
	path, content, known := loadConfig(env, a.Detect(env), a.path)
	if known != nil {
		return path, content, *known
	}
	_, exists, err := a.merge(content, want.Config(), false)
	if err != nil {
		return path, content, Inspection{Err: err}
	}
	if !exists {
		return path, content, Inspection{State: Missing}
	}
	var root map[string]any
	serversKey := "mcpServers"
	if a.toml {
		serversKey = "mcp_servers"
		err = toml.Unmarshal(content, &root)
	} else {
		err = json.Unmarshal(content, &root)
	}
	servers, _ := root[serversKey].(map[string]any)
	current := servers[aulaglobalKey]
	if sameEntry(current, want.Config()) {
		return path, content, Inspection{State: Identical}
	}
	if a.ignoreDisabledFalse {
		if currentObject, ok := current.(map[string]any); ok && currentObject["disabled"] == false {
			delete(currentObject, "disabled")
			if sameEntry(currentObject, want.Config()) {
				return path, content, Inspection{State: Identical}
			}
		}
	}
	detail := "entrada existente"
	if currentObject, ok := current.(map[string]any); ok {
		if command, ok := currentObject["command"].(string); ok {
			detail = "comando " + fmt.Sprintf("%q", command)
		}
	}
	return path, content, Inspection{State: Different, Detail: detail, Err: err}
}

func (a fileAssistant) Inspect(env Env, want Entry) Inspection {
	_, _, inspection := a.read(env, want)
	return inspection
}

func (a fileAssistant) Apply(env Env, want Entry, replace bool) error {
	path, content, inspection := a.read(env, want)
	if inspection.Err != nil {
		return inspection.Err
	}
	if inspection.State == NotInstalled {
		return errors.New("not installed")
	}
	if inspection.State == Identical {
		return nil
	}
	if inspection.State == Different && !replace {
		return errors.New("replacement not authorized")
	}
	entry := want.Config()
	if a.generate != nil {
		var err error
		entry, err = a.generate(env, want)
		if err != nil {
			return err
		}
	}
	merged, _, err := a.merge(content, entry, replace)
	if err != nil {
		return err
	}
	return env.write(path, merged)
}

func (a fileAssistant) Manual(want Entry) string {
	example, _, _ := a.merge(nil, want.Config(), true)
	return "Alternativa manual · " + a.name + ":\n" + string(example)
}

func (a fileAssistant) Find(env Env) Link {
	return findInConfig(env, a.Detect(env), a.path, a.entryOf, "no se puede interpretar con seguridad")
}

func (a fileAssistant) Remove(env Env) error {
	return removeFromConfig(env, a.path, a.removeFrom)
}

func (a fileAssistant) ManualRemove(env Env) string {
	path, _ := a.path(env)
	if path == "" {
		path = a.name
	}
	if a.toml {
		return "Alternativa manual · " + a.name + ": abre " + path + " y borra la tabla [mcp_servers.aulaglobal] (y [mcp_servers.aulaglobal.env] si existe)."
	}
	return "Alternativa manual · " + a.name + ": abre " + path + " y borra la clave «aulaglobal» dentro de «mcpServers»."
}

// findInConfig is Find for an assistant with one configuration file. entryOf
// returns the aulaglobal entry in the file's bytes, and unsafeDetail explains
// why a file it cannot interpret is left alone.
func findInConfig(env Env, installed bool, configPath func(Env) (string, error), entryOf func([]byte) (map[string]any, bool, error), unsafeDetail string) Link {
	if !installed {
		return Link{State: LinkNotInstalled}
	}
	path, err := configPath(env)
	if err != nil {
		return Link{State: LinkError, Detail: err.Error()}
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Link{State: LinkNone, Location: path}
	}
	if err != nil {
		return Link{State: LinkError, Location: path, Detail: err.Error()}
	}
	entry, found, err := entryOf(content)
	if err != nil {
		return Link{State: LinkError, Location: path, Detail: unsafeDetail}
	}
	if !found {
		return Link{State: LinkNone, Location: path}
	}
	link := linkFromEntry(entry)
	link.Location = path
	return link
}

// removeFromConfig is Remove for an assistant with one configuration file.
// removeFrom drops the aulaglobal entry from the file's bytes and reports
// whether it was there; a file without the entry is left untouched.
func removeFromConfig(env Env, configPath func(Env) (string, error), removeFrom func([]byte) ([]byte, bool, error)) error {
	path, err := configPath(env)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	remaining, found, err := removeFrom(content)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return env.write(path, remaining)
}
