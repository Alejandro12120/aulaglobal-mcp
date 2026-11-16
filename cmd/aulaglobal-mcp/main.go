// Command aulaglobal-mcp is an MCP server for the UC3M Moodle "AulaGlobal". It
// also has the subcommands setup, update, uninstall and token-from-cookie.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"aulaglobal-mcp/internal/aulaglobal"
	"aulaglobal-mcp/internal/cli/serve"
	"aulaglobal-mcp/internal/cli/setup"
	"aulaglobal-mcp/internal/cli/uninstall"
	"aulaglobal-mcp/internal/cli/update"
	"aulaglobal-mcp/internal/selfupdate"
	"aulaglobal-mcp/internal/userpaths"

	"golang.org/x/term"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	selfupdate.CleanupOld()
	if err := run(); err != nil {
		slog.Error("server stopped", "error", aulaglobal.Redact(err.Error(), os.Getenv("AULAGLOBAL_TOKEN"), os.Getenv("MCP_HTTP_AUTH_TOKEN")))
		os.Exit(1)
	}
}

// run dispatches on the first argument. Anything that is not a subcommand
// starts the MCP server. To add a subcommand, add a case here and a package
// under internal/cli with its implementation.
func run() error {
	// Double-clicking the program on Windows opens a console with no arguments.
	if runtime.GOOS == "windows" && len(os.Args) == 1 && term.IsTerminal(int(os.Stdin.Fd())) {
		return setup.Run(nil)
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "version":
			fmt.Println(version)
			return nil
		case "setup":
			return setup.Run(os.Args[2:])
		case "update":
			return update.Run(os.Args[2:], version)
		case "uninstall":
			return uninstall.Run(os.Args[2:])
		}
	}
	api, defaultData, err := newClient()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if len(os.Args) > 1 && os.Args[1] == "token-from-cookie" {
		return setup.TokenFromCookie(ctx, api, os.Args[2:])
	}
	return serve.Run(ctx, api, defaultData, version)
}

// newClient builds the AulaGlobal client from the environment. The token comes
// from AULAGLOBAL_TOKEN, or from AULAGLOBAL_TOKEN_FILE, or from the default
// token file if it exists. It also returns the default download folder.
func newClient() (*aulaglobal.Client, string, error) {
	defaultToken, defaultData, err := userpaths.Defaults()
	if err != nil {
		if os.Getenv("AULAGLOBAL_DOWNLOAD_DIR") == "" {
			return nil, "", err
		}
	}
	tokenFile := os.Getenv("AULAGLOBAL_TOKEN_FILE")
	if tokenFile == "" && os.Getenv("AULAGLOBAL_TOKEN") == "" {
		if _, statErr := os.Stat(defaultToken); statErr == nil {
			tokenFile = defaultToken
		}
	}
	return aulaglobal.New(aulaglobal.Site, aulaglobal.StaticToken{Value: os.Getenv("AULAGLOBAL_TOKEN"), File: tokenFile}), defaultData, nil
}
