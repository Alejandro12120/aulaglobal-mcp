// Package userpaths defines locations independent of the working directory.
package userpaths

import (
	"os"
	"path/filepath"
)

func Defaults() (token, data string, err error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	return filepath.Join(config, "aulaglobal-mcp", "token"), filepath.Join(home, "AulaGlobal"), nil
}

func Claude(goos, home, config string) string {
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	}
	return filepath.Join(config, "Claude", "claude_desktop_config.json")
}

// OpenCode uses XDG on every OS, including Windows and macOS.
func OpenCode(home, xdg string) string {
	if xdg == "" || !filepath.IsAbs(xdg) {
		xdg = filepath.Join(home, ".config")
	}
	return filepath.Join(xdg, "opencode")
}

// Antigravity uses the same home-relative global config on every OS.
func Antigravity(home string) string {
	return filepath.Join(home, ".gemini", "config", "mcp_config.json")
}
