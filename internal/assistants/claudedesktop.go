package assistants

import (
	"path/filepath"

	"aulaglobal-mcp/internal/userpaths"
)

func desktopAssistant() Assistant {
	return fileAssistant{
		id:     "claude-desktop",
		name:   "Claude Desktop",
		reload: "Cierra y vuelve a abrir Claude Desktop.",
		path:   func(env Env) (string, error) { return userpaths.Claude(env.GOOS, env.Home, env.ConfigDir), nil },
		detect: func(env Env) bool {
			return dirExists(env, filepath.Dir(userpaths.Claude(env.GOOS, env.Home, env.ConfigDir)))
		},
		merge:      mcpServersConfig,
		entryOf:    mcpServersEntry,
		removeFrom: mcpServersRemove,
	}
}
