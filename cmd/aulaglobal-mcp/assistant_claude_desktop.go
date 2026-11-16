package main

import (
	"path/filepath"

	"aulaglobal-mcp/internal/userpaths"
)

func desktopAssistant() assistant {
	return fileAssistant{
		id:     "claude-desktop",
		name:   "Claude Desktop",
		reload: "Cierra y vuelve a abrir Claude Desktop.",
		path:   func(e clientEnv) (string, error) { return userpaths.Claude(e.GOOS, e.Home, e.Config), nil },
		detect: func(e clientEnv) bool {
			return dirExists(e, filepath.Dir(userpaths.Claude(e.GOOS, e.Home, e.Config)))
		},
		merge:      mcpServersConfig,
		entryOf:    mcpServersEntry,
		removeFrom: mcpServersRemove,
	}
}
