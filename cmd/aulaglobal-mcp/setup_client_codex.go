package main

import (
	"path/filepath"
)

func codexAssistant() assistant {
	return fileAssistant{id: "codex", name: "Codex / ChatGPT", reload: "Reinicia Codex / ChatGPT o abre una sesión nueva.", toml: true, path: func(e clientEnv) (string, error) { return filepath.Join(e.Home, ".codex", "config.toml"), nil }, detect: func(e clientEnv) bool { return dirExists(e, filepath.Join(e.Home, ".codex")) }, merge: codexConfig}
}
