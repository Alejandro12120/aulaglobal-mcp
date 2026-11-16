package main

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

// Each AI assistant has its own file, assistant_<id>.go, with everything that
// is specific to it: detecting it, reading and writing its configuration, and
// removing the entry again. To support one more assistant, implement
// `assistant` in a new file and append it to `assistants`.

type clientState int

const (
	notInstalledHere clientState = iota
	missing
	identical
	different
	otherScope
)

// inspection is what Inspect reports about the aulaglobal entry of one assistant.
type inspection struct {
	state         clientState
	detail, scope string
	err           error
}

// desiredEntry is the entry setup wants every assistant to hold: this program
// run over stdio. Extra holds "-e", "NAME=value" pairs for the environment
// variables that differ from the defaults.
type desiredEntry struct {
	Exe   string
	Extra []string
}

func (w desiredEntry) entry() map[string]any {
	e := map[string]any{"command": w.Exe, "args": []string{"--transport", "stdio"}}
	if env := envMap(w.Extra); len(env) > 0 {
		e["env"] = env
	}
	return e
}

// approvedEnv returns the NAME=value pairs in extra that this program defines.
// Anything else is dropped so that no other variable reaches an assistant's
// configuration.
func approvedEnv(extra []string) []string {
	var pairs []string
	for i := 0; i+1 < len(extra); i += 2 {
		k, _, ok := strings.Cut(extra[i+1], "=")
		if ok && (k == "AULAGLOBAL_TOKEN_FILE" || k == "AULAGLOBAL_DOWNLOAD_DIR") {
			pairs = append(pairs, extra[i+1])
		}
	}
	return pairs
}

func envMap(extra []string) map[string]string {
	env := map[string]string{}
	for _, pair := range approvedEnv(extra) {
		k, v, _ := strings.Cut(pair, "=")
		env[k] = v
	}
	return env
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

// clientEnv holds everything assistants read from the machine, so tests can
// replace the file system, the PATH lookup and the subprocesses.
type clientEnv struct {
	Write                                          func(string, []byte) error
	GOOS, Home, Config, AppData, LocalAppData, XDG string
	Look                                           func(string) (string, error)
	Stat                                           func(string) (os.FileInfo, error)
	Run                                            cliRunner // Claude Code CLI
	OpenRun                                        cliRunner // OpenCode CLI
	GenerateAG                                     func(string, string, []string) (map[string]any, error)
}

func realClientEnv(goos, home, config string) clientEnv {
	return clientEnv{GOOS: goos, Home: home, Config: config, AppData: os.Getenv("APPDATA"), LocalAppData: os.Getenv("LOCALAPPDATA"), XDG: os.Getenv("XDG_CONFIG_HOME"), Look: exec.LookPath, Stat: os.Stat, Run: runClaude, OpenRun: runOpenCode, GenerateAG: antigravityCLIEntry}
}

type cliRunner func(context.Context, string, ...string) ([]byte, error)

// call runs a CLI with its own 30 second deadline. Callers must not print the
// output: a third-party CLI may include private configuration in diagnostics.
func (e clientEnv) call(run cliRunner, cli string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return run(ctx, cli, args...)
}

func (e clientEnv) write(path string, b []byte) error {
	if e.Write != nil {
		return e.Write(path, b)
	}
	return atomicConfig(path, b)
}

func dirExists(e clientEnv, p string) bool { f, err := e.Stat(p); return err == nil && f.IsDir() }

// isExecutable reports whether p is a regular file the user can run. Windows has
// no execute bit, so any file counts there.
func isExecutable(goos string, stat func(string) (os.FileInfo, error), p string) bool {
	f, err := stat(p)
	return err == nil && !f.IsDir() && (goos == "windows" || f.Mode()&0111 != 0)
}

type assistant interface {
	ID() string
	Name() string
	Detect(clientEnv) bool
	Inspect(clientEnv, desiredEntry) inspection
	Apply(clientEnv, desiredEntry, bool) error
	Manual(desiredEntry) string
	Reload() string
	// Find, Remove and ManualRemove serve `uninstall`. They look for the entry
	// named aulaglobal and remove only that entry, never anything else.
	Find(clientEnv) link
	Remove(clientEnv) error
	ManualRemove(clientEnv) string
}

// assistants is the order used by setup, by --clients and by the help text.
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

// aulaglobalKey is the name every assistant stores this server under.
const aulaglobalKey = "aulaglobal"

// linkFromEntry reads command and env from any assistant's entry shape: the
// "command" is a string, or an array in OpenCode, and the variables are under
// "env" or "environment".
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

func sameEntry(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
