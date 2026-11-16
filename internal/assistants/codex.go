package assistants

import (
	"bufio"
	"bytes"
	"errors"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func codexAssistant() Assistant {
	return fileAssistant{
		id:     "codex",
		name:   "Codex / ChatGPT",
		reload: "Reinicia Codex / ChatGPT o abre una sesión nueva.",
		toml:   true,
		path:   func(env Env) (string, error) { return filepath.Join(env.Home, ".codex", "config.toml"), nil },
		detect: func(env Env) bool { return dirExists(env, filepath.Join(env.Home, ".codex")) },

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
	if err := toml.Unmarshal(old, &root); err != nil {
		return nil, false, err
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	_, exists := servers[aulaglobalKey]
	if exists && !replace {
		return nil, true, nil
	}
	block, err := toml.Marshal(map[string]any{"mcp_servers": map[string]any{aulaglobalKey: entry}})
	if err != nil {
		return nil, exists, err
	}
	// When replacing, the parent table header is already in the file.
	block = []byte(strings.TrimPrefix(string(block), "[mcp_servers]\n"))
	if !exists {
		out := append(append([]byte{}, old...), '\n')
		out = append(out, block...)
		var check map[string]any
		err = toml.Unmarshal(out, &check)
		return out, false, err
	}
	if hasMultilineString(old) {
		return nil, true, errors.New("El TOML contiene texto multilínea. Añade aulaglobal manualmente; no se ha cambiado nada.")
	}
	lines, found, _, err := withoutCodexTable(old)
	if err != nil {
		return nil, true, err
	}
	if !found {
		return nil, true, errors.New("Formato TOML no sustituible con seguridad; edítalo manualmente")
	}
	out := joinLines(lines) + string(block)
	var check map[string]any
	err = toml.Unmarshal([]byte(out), &check)
	return []byte(out), true, err
}

func codexEntry(old []byte) (map[string]any, bool, error) {
	var root map[string]any
	if err := toml.Unmarshal(old, &root); err != nil {
		return nil, false, err
	}
	servers, _ := root["mcp_servers"].(map[string]any)
	value, ok := servers[aulaglobalKey]
	if !ok {
		return nil, false, nil
	}
	entry, ok := value.(map[string]any)
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
	if _, still, err := codexEntry([]byte(out)); err != nil || still {
		return nil, true, errors.New("unsupported TOML layout")
	}
	return []byte(out), true, nil
}

func hasMultilineString(data []byte) bool {
	return bytes.Contains(data, []byte(`"""`)) || bytes.Contains(data, []byte("'''"))
}

// withoutCodexTable returns the lines of old except the [mcp_servers.aulaglobal]
// table and its sub-tables. found reports whether such a table was there, and
// atEnd whether it was the last thing in the file.
func withoutCodexTable(old []byte) (kept []string, found, atEnd bool, err error) {
	scanner := bufio.NewScanner(bytes.NewReader(old))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	skipping := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			var header map[string]any
			if err := toml.Unmarshal([]byte(line+"\n"), &header); err != nil {
				return nil, false, false, err
			}
			servers, _ := header["mcp_servers"].(map[string]any)
			_, skipping = servers[aulaglobalKey]
			found = found || skipping
		}
		if !skipping {
			kept = append(kept, line)
		}
	}
	return kept, found, skipping, scanner.Err()
}

// joinLines is the inverse of splitting a file into lines: every line ends
// with a newline.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
