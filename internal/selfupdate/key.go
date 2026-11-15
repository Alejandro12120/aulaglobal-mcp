// Package selfupdate implements the signed self-update from GitHub Releases and
// the passive "new version available" notice.
package selfupdate

import (
	_ "embed"
	"strings"
)

const (
	// Repo is the GitHub repository that publishes the releases.
	Repo = "Alejandro12120/aulaglobal-mcp"
	// Namespace binds signatures to this purpose (ssh-keygen -n).
	Namespace = "aulaglobal-mcp-update"
	// BinaryName is the executable inside each release archive (without .exe).
	BinaryName = "aulaglobal-mcp"
)

// update_key.pub is the maintainer's public signing key in authorized_keys
// format. The release workflow reads the same file, so CI and the binary can
// never disagree about which key is trusted.
//
//go:embed update_key.pub
var embeddedKey string

// TrustedKey returns the embedded public key (authorized_keys line).
func TrustedKey() string { return strings.TrimSpace(embeddedKey) }
