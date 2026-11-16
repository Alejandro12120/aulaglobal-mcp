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
		path:   func(e Env) (string, error) { return userpaths.Claude(e.GOOS, e.Home, e.ConfigDir), nil },
		detect: func(e Env) bool {
			return dirExists(e, filepath.Dir(userpaths.Claude(e.GOOS, e.Home, e.ConfigDir)))
		},
		merge:      mcpServersConfig,
		entryOf:    mcpServersEntry,
		removeFrom: mcpServersRemove,
	}
}
