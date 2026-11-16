// Package assistants knows how to connect this program to each AI assistant.
//
// Each assistant has its own file with everything specific to it: detecting
// it, reading and writing its configuration, and removing the entry again. To
// support one more assistant, implement Assistant in a new file and append it
// to registry.
package assistants

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// State is what an assistant's configuration holds for the aulaglobal entry,
// compared with the entry setup wants.
type State int

const (
	NotInstalled State = iota // the assistant is not on this computer
	Missing                   // installed, no aulaglobal entry
	Identical                 // the entry is already what setup wants
	Different                 // an entry exists but differs
	OtherScope                // Claude Code only: defined in a local or project scope
)

// Inspection is what Inspect reports about the aulaglobal entry of one assistant.
type Inspection struct {
	State  State
	Detail string // what differs, or why the entry is in another scope
	Scope  string // Claude Code scope: "local", "project" or "user"
	Err    error  // set when the configuration cannot be read or changed safely
}

// Entry is the entry setup wants every assistant to hold: this program run
// over stdio. Extra holds "-e", "NAME=value" pairs for the environment
// variables that differ from the defaults.
type Entry struct {
	Exe   string
	Extra []string
}

// NewEntry builds the entry for the executable exe. The token file and data
// folder are only added to the entry when they differ from their defaults.
func NewEntry(exe, tokenFile, dataDir, defaultTokenFile, defaultDataDir string) (Entry, error) {
	absExe, err := filepath.Abs(exe)
	if err != nil {
		return Entry{}, err
	}
	entry := Entry{Exe: absExe}
	for _, v := range []struct{ name, value, fallback string }{
		{"AULAGLOBAL_TOKEN_FILE", tokenFile, defaultTokenFile},
		{"AULAGLOBAL_DOWNLOAD_DIR", dataDir, defaultDataDir},
	} {
		if v.value != v.fallback {
			entry.Extra = append(entry.Extra, "-e", v.name+"="+v.value)
		}
	}
	return entry, nil
}

// Config returns the entry as the JSON object most assistants store.
func (entry Entry) Config() map[string]any {
	config := map[string]any{"command": entry.Exe, "args": []string{"--transport", "stdio"}}
	if env := envMap(entry.Extra); len(env) > 0 {
		config["env"] = env
	}
	return config
}

// approvedEnv returns the NAME=value pairs in extra that this program defines.
// Anything else is dropped so that no other variable reaches an assistant's
// configuration.
func approvedEnv(extra []string) []string {
	var pairs []string
	for i := 0; i+1 < len(extra); i += 2 {
		name, _, ok := strings.Cut(extra[i+1], "=")
		if ok && (name == "AULAGLOBAL_TOKEN_FILE" || name == "AULAGLOBAL_DOWNLOAD_DIR") {
			pairs = append(pairs, extra[i+1])
		}
	}
	return pairs
}

func envMap(extra []string) map[string]string {
	env := map[string]string{}
	for _, pair := range approvedEnv(extra) {
		name, value, _ := strings.Cut(pair, "=")
		env[name] = value
	}
	return env
}

// Env holds everything assistants read from the machine, so tests can replace
// the file system, the PATH lookup and the subprocesses.
type Env struct {
	GOOS, Home, ConfigDir, AppData, LocalAppData, XDGConfigHome string
	WriteConfig                                                 func(path string, data []byte) error
	LookPath                                                    func(name string) (string, error)
	Stat                                                        func(path string) (os.FileInfo, error)
	RunClaude                                                   Runner
	RunOpenCode                                                 Runner
	GenerateAntigravity                                         func(cli, exe string, extra []string) (map[string]any, error)
}

// RealEnv is the Env of this computer.
func RealEnv(goos, home, configDir string) Env {
	return Env{
		GOOS: goos, Home: home, ConfigDir: configDir,
		AppData: os.Getenv("APPDATA"), LocalAppData: os.Getenv("LOCALAPPDATA"), XDGConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		LookPath: exec.LookPath, Stat: os.Stat,
		RunClaude: runClaude, RunOpenCode: runOpenCode, GenerateAntigravity: antigravityCLIEntry,
	}
}

// Runner runs a command line tool and returns its combined output.
type Runner func(ctx context.Context, cli string, args ...string) ([]byte, error)

// call runs a CLI with its own 30 second deadline. Callers must not print the
// output: a third-party CLI may include private configuration in diagnostics.
func (env Env) call(run Runner, cli string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return run(ctx, cli, args...)
}

func (env Env) write(path string, data []byte) error {
	if env.WriteConfig != nil {
		return env.WriteConfig(path, data)
	}
	return atomicConfig(path, data)
}

func dirExists(env Env, path string) bool {
	info, err := env.Stat(path)
	return err == nil && info.IsDir()
}

// isExecutable reports whether path is a regular file the user can run. Windows
// has no execute bit, so any file counts there.
func isExecutable(goos string, stat func(string) (os.FileInfo, error), path string) bool {
	info, err := stat(path)
	return err == nil && !info.IsDir() && (goos == "windows" || info.Mode()&0111 != 0)
}

// Assistant is one AI assistant this program can connect to.
type Assistant interface {
	ID() string
	Name() string
	Detect(Env) bool
	// Inspect compares the assistant's configuration with the wanted entry.
	Inspect(Env, Entry) Inspection
	// Apply adds the entry. An existing entry is only replaced when replace is true.
	Apply(env Env, entry Entry, replace bool) error
	// Manual is the instructions to add the entry by hand.
	Manual(Entry) string
	// Reload tells the user how to make the assistant pick up the change.
	Reload() string
	// Find, Remove and ManualRemove serve `uninstall`. They look for the entry
	// named aulaglobal and remove only that entry, never anything else.
	Find(Env) Link
	Remove(Env) error
	ManualRemove(Env) string
}

// registry is the order used by setup, by --clients and by the help text.
var registry = []Assistant{claudeAssistant{}, desktopAssistant(), codexAssistant(), openCodeAssistant{}, antigravityAssistant()}

// All returns every supported assistant, in the order setup lists them.
func All() []Assistant { return registry }

// Select returns the assistants named in the comma separated ids, in registry
// order, or all of them when ids is empty.
func Select(ids string) ([]Assistant, error) {
	if ids == "" {
		return registry, nil
	}
	wanted := map[string]bool{}
	for _, id := range strings.Split(ids, ",") {
		wanted[strings.TrimSpace(id)] = true
	}
	var selected []Assistant
	for _, assistant := range registry {
		if wanted[assistant.ID()] {
			selected = append(selected, assistant)
			delete(wanted, assistant.ID())
		}
	}
	if len(wanted) > 0 {
		return nil, errors.New("Cliente desconocido; usa --help para consultar los IDs válidos")
	}
	return selected, nil
}

// IDs returns the IDs of all assistants, comma separated.
func IDs() string {
	var ids []string
	for _, assistant := range registry {
		ids = append(ids, assistant.ID())
	}
	return strings.Join(ids, ",")
}

// LinkState says what an assistant's configuration holds for "aulaglobal".
type LinkState int

const (
	LinkNotInstalled LinkState = iota // assistant not found on this computer
	LinkNone                          // installed, no aulaglobal entry
	LinkPresent                       // an entry we can remove
	LinkElsewhere                     // Claude Code local/project scope: explained, never touched
	LinkError                         // cannot be read or modified safely
)

// Link is what Find reports about one assistant. Command and Env come from the
// entry itself so the caller can tell whether it points at this program.
type Link struct {
	State    LinkState
	Command  string
	Env      map[string]string
	Detail   string // explanation for LinkElsewhere and LinkError
	Location string // file or command shown in the plan
}

// aulaglobalKey is the name every assistant stores this server under.
const aulaglobalKey = "aulaglobal"

// linkFromEntry reads command and env from any assistant's entry shape: the
// "command" is a string, or an array in OpenCode, and the variables are under
// "env" or "environment".
func linkFromEntry(entry map[string]any) Link {
	link := Link{State: LinkPresent, Env: map[string]string{}}
	switch command := entry["command"].(type) {
	case string:
		link.Command = command
	case []any:
		if len(command) > 0 {
			link.Command, _ = command[0].(string)
		}
	}
	for _, key := range []string{"env", "environment"} {
		if vars, ok := entry[key].(map[string]any); ok {
			for name, value := range vars {
				if text, ok := value.(string); ok {
					link.Env[name] = text
				}
			}
		}
	}
	return link
}

func sameEntry(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
