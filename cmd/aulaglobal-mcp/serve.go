package main

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
	"aulaglobal-mcp/internal/mcpserver"
	"aulaglobal-mcp/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serve runs the MCP server over stdio or HTTP until ctx is cancelled.
func serve(ctx context.Context, api *aulaglobal.Client, defaultData string) error {
	transport := flag.String("transport", env("MCP_TRANSPORT", "stdio"), "stdio or http")
	addr := flag.String("addr", env("MCP_ADDR", "127.0.0.1:8080"), "HTTP bind address")
	unsafe := flag.Bool("dangerously-disable-http-auth", false, "DANGER: allow unauthenticated access to course files")
	hosts := flag.String("allowed-hosts", os.Getenv("MCP_ALLOWED_HOSTS"), "comma separated exact Host headers; default localhost:8080,127.0.0.1:8080")
	origins := flag.String("allowed-origins", os.Getenv("MCP_ALLOWED_ORIGINS"), "comma separated exact allowed Origin headers; default none")
	flag.Parse()
	workers, e := number("AULAGLOBAL_CONCURRENCY", 4)
	if e != nil {
		return e
	}
	maxBytes, e := number("AULAGLOBAL_MAX_FILE_BYTES", 256<<20)
	if e != nil {
		return e
	}
	stall, e := number("AULAGLOBAL_DOWNLOAD_STALL_SECONDS", 60)
	if e != nil {
		return e
	}
	if stall < 1 || stall > 3600 {
		return errors.New("stall seconds must be 1..3600")
	}
	api.StallTimeout = time.Duration(stall) * time.Second
	if v := os.Getenv("AULAGLOBAL_MAX_RPS"); v != "" {
		rps, e := aulaglobal.ParseMaxRPS(v)
		if e != nil {
			return e
		}
		if e = api.SetMaxRPS(rps); e != nil {
			return e
		}
	}
	st, e := store.Open(env("AULAGLOBAL_DOWNLOAD_DIR", defaultData), maxBytes, int(workers))
	if e != nil {
		return e
	}
	defer st.Root.Close()
	server := mcpserver.New(api, st, startUpdateCheck(ctx))
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
func number(k string, d int64) (int64, error) {
	v := os.Getenv(k)
	if v == "" {
		return d, nil
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil {
		return 0, fmt.Errorf("invalid %s", k)
	}
	return n, nil
}

// serveHTTP serves the MCP endpoint at /mcp. Unless unsafe is set it requires
// the bearer token from MCP_HTTP_AUTH_TOKEN or MCP_HTTP_AUTH_TOKEN_FILE.
func serveHTTP(ctx context.Context, server *mcp.Server, addr, hosts, origins string, unsafe bool) error {
	token := strings.TrimSpace(os.Getenv("MCP_HTTP_AUTH_TOKEN"))
	if f := os.Getenv("MCP_HTTP_AUTH_TOKEN_FILE"); f != "" {
		b, e := os.ReadFile(f)
		if e != nil {
			return errors.New("cannot read HTTP auth token file")
		}
		token = strings.TrimSpace(string(b))
	}
	if !unsafe && token == "" {
		return errors.New("HTTP requires MCP_HTTP_AUTH_TOKEN or MCP_HTTP_AUTH_TOKEN_FILE")
	}
	if unsafe {
		slog.Warn("DANGER: HTTP authentication disabled; clients can access all downloaded course files")
	}
	_, port, e := net.SplitHostPort(addr)
	if e != nil {
		return e
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
