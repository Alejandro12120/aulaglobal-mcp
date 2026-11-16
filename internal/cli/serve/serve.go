package serve

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"aulaglobal-mcp/internal/aulaglobal"
	"aulaglobal-mcp/internal/cli/env"
	"aulaglobal-mcp/internal/mcpserver"
	"aulaglobal-mcp/internal/selfupdate"
	"aulaglobal-mcp/internal/store"
	"aulaglobal-mcp/internal/userpaths"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run runs the MCP server over stdio or HTTP until ctx is cancelled. version
// is the version of the running program, which the update check compares.
func Run(ctx context.Context, api *aulaglobal.Client, defaultData, version string) error {
	transport := flag.String("transport", env.Or("MCP_TRANSPORT", "stdio"), "stdio or http")
	addr := flag.String("addr", env.Or("MCP_ADDR", "127.0.0.1:8080"), "HTTP bind address")
	unsafe := flag.Bool("dangerously-disable-http-auth", false, "DANGER: allow unauthenticated access to course files")
	hosts := flag.String("allowed-hosts", os.Getenv("MCP_ALLOWED_HOSTS"), "comma separated exact Host headers; default localhost:8080,127.0.0.1:8080")
	origins := flag.String("allowed-origins", os.Getenv("MCP_ALLOWED_ORIGINS"), "comma separated exact allowed Origin headers; default none")
	flag.Parse()
	workers, err := number("AULAGLOBAL_CONCURRENCY", 4)
	if err != nil {
		return err
	}
	maxBytes, err := number("AULAGLOBAL_MAX_FILE_BYTES", 256<<20)
	if err != nil {
		return err
	}
	stall, err := number("AULAGLOBAL_DOWNLOAD_STALL_SECONDS", 60)
	if err != nil {
		return err
	}
	if stall < 1 || stall > 3600 {
		return errors.New("stall seconds must be 1..3600")
	}
	api.StallTimeout = time.Duration(stall) * time.Second
	if maxRPSText := os.Getenv("AULAGLOBAL_MAX_RPS"); maxRPSText != "" {
		rps, err := aulaglobal.ParseMaxRPS(maxRPSText)
		if err != nil {
			return err
		}
		if err = api.SetMaxRPS(rps); err != nil {
			return err
		}
	}
	dataStore, err := store.Open(env.Or("AULAGLOBAL_DOWNLOAD_DIR", defaultData), maxBytes, int(workers))
	if err != nil {
		return err
	}
	defer dataStore.Root.Close()
	server := mcpserver.New(api, dataStore, startUpdateCheck(ctx, version))
	switch *transport {
	case "stdio":
		return server.Run(ctx, &mcp.StdioTransport{})
	case "http":
		return serveHTTP(ctx, server, *addr, *hosts, *origins, *unsafe)
	default:
		return errors.New("transport must be stdio or http")
	}
}

// number reads an integer from the environment variable k, or returns d.
func number(name string, fallback int64) (int64, error) {
	text := os.Getenv(name)
	if text == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}

// serveHTTP serves the MCP endpoint at /mcp. Unless unsafe is set it requires
// the bearer token from MCP_HTTP_AUTH_TOKEN or MCP_HTTP_AUTH_TOKEN_FILE.
func serveHTTP(ctx context.Context, server *mcp.Server, addr, hosts, origins string, unsafe bool) error {
	token := strings.TrimSpace(os.Getenv("MCP_HTTP_AUTH_TOKEN"))
	if tokenFile := os.Getenv("MCP_HTTP_AUTH_TOKEN_FILE"); tokenFile != "" {
		content, err := os.ReadFile(tokenFile)
		if err != nil {
			return errors.New("cannot read HTTP auth token file")
		}
		token = strings.TrimSpace(string(content))
	}
	if !unsafe && token == "" {
		return errors.New("HTTP requires MCP_HTTP_AUTH_TOKEN or MCP_HTTP_AUTH_TOKEN_FILE")
	}
	if unsafe {
		slog.Warn("DANGER: HTTP authentication disabled; clients can access all downloaded course files")
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	allowed := []string{net.JoinHostPort("localhost", port), net.JoinHostPort("127.0.0.1", port)}
	if hosts != "" {
		allowed = strings.Split(hosts, ",")
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpserver.SecureHTTP(handler, token, unsafe, allowed, strings.Split(origins, ",")))
	// Go 1.26 clears ReadTimeout after the body is read, before its background
	// read starts. So ReadTimeout bounds reading the body without limiting how
	// long a tool call may run.
	httpServer := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan error, 1)
	go func() { done <- httpServer.ListenAndServe() }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdown)
	}
}

// startUpdateCheck launches the single background check for the MCP server and
// returns the notifier, or nil when checks are disabled.
func startUpdateCheck(ctx context.Context, version string) mcpserver.Updates {
	if !selfupdate.CheckEnabled(version) {
		return nil
	}
	cache, _ := userpaths.UpdateCache() // an error just disables caching
	checker, err := selfupdate.NewChecker(version, cache)
	if err != nil {
		return nil
	}
	go checker.Run(ctx)
	return checker
}
