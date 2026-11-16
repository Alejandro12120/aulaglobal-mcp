package main

import (
	"bufio"
	"bytes"
	"errors"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func codexAssistant() assistant {
	return fileAssistant{
		id:     "codex",
		name:   "Codex / ChatGPT",
		reload: "Reinicia Codex / ChatGPT o abre una sesión nueva.",
		toml:   true,
		path:   func(e clientEnv) (string, error) { return filepath.Join(e.Home, ".codex", "config.toml"), nil },
		detect: func(e clientEnv) bool { return dirExists(e, filepath.Join(e.Home, ".codex")) },

		merge:      codexConfig,
		entryOf:    codexEntry,
		removeFrom: codexRemove,
	}
}

// codexConfig sets [mcp_servers.aulaglobal] in config.toml. A new entry is
// appended, which keeps comments and every other byte. Replacing an entry cuts
// its table out line by line, so it is refused for files with multiline strings,
// where a line that looks like a table header may be text. The user can edit
// that unusual file by hand.
func codexConfig(old []byte, entry map[string]any, replace bool) ([]byte, bool, error) {
	var root map[string]any
	if e := toml.Unmarshal(old, &root); e != nil {
		return nil, false, e
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	_, exists := servers[aulaglobalKey]
	if exists && !replace {
		return nil, true, nil
	}
	block, e := toml.Marshal(map[string]any{"mcp_servers": map[string]any{aulaglobalKey: entry}})
	if e != nil {
		return nil, exists, e
	}
	// When replacing, the parent table header is already in the file.
	block = []byte(strings.TrimPrefix(string(block), "[mcp_servers]\n"))
	if !exists {
		out := append(append([]byte{}, old...), '\n')
		out = append(out, block...)
		var check map[string]any
		e = toml.Unmarshal(out, &check)
		return out, false, e
	}
	if hasMultilineString(old) {
		return nil, true, errors.New("El TOML contiene texto multilínea. Añade aulaglobal manualmente; no se ha cambiado nada.")
	}
	lines, found, _, e := withoutCodexTable(old)
	if e != nil {
		return nil, true, e
	}
	if !found {
		return nil, true, errors.New("Formato TOML no sustituible con seguridad; edítalo manualmente")
	}
	out := joinLines(lines) + string(block)
	var check map[string]any
	e = toml.Unmarshal([]byte(out), &check)
	return []byte(out), true, e
}

func codexEntry(old []byte) (map[string]any, bool, error) {
	var root map[string]any
	if e := toml.Unmarshal(old, &root); e != nil {
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
	if hasMultilineString(old) {
		return nil, true, errors.New("multiline TOML")
	}
	lines, _, removedAtEnd, err := withoutCodexTable(old)
	if err != nil {
		return nil, true, err
	}
	if removedAtEnd { // the blank line setup put before our block goes with it
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
	}
	out := joinLines(lines)
	if _, still, e := codexEntry([]byte(out)); e != nil || still {
		return nil, true, errors.New("unsupported TOML layout")
	}
	return []byte(out), true, nil
}

func hasMultilineString(b []byte) bool {
	return bytes.Contains(b, []byte(`"""`)) || bytes.Contains(b, []byte("'''"))
}

// withoutCodexTable returns the lines of old except the [mcp_servers.aulaglobal]
// table and its sub-tables. found reports whether such a table was there, and
// atEnd whether it was the last thing in the file.
func withoutCodexTable(old []byte) (kept []string, found, atEnd bool, err error) {
	sc := bufio.NewScanner(bytes.NewReader(old))
	sc.Buffer(make([]byte, 4096), 4<<20)
	skipping := false
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			var header map[string]any
			if e := toml.Unmarshal([]byte(line+"\n"), &header); e != nil {
				return nil, false, false, e
			}
			servers, _ := header["mcp_servers"].(map[string]any)
			_, skipping = servers[aulaglobalKey]
			found = found || skipping
		}
		if !skipping {
			kept = append(kept, line)
		}
	}
	return kept, found, skipping, sc.Err()
}

// joinLines is the inverse of splitting a file into lines: every line ends
// with a newline.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
