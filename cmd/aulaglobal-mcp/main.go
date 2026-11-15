package main

import (
	"aulaglobal-mcp/internal/aulaglobal"
	"aulaglobal-mcp/internal/mcpserver"
	"aulaglobal-mcp/internal/selfupdate"
	"aulaglobal-mcp/internal/store"
	"aulaglobal-mcp/internal/userpaths"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/term"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
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

var version = "dev"

func run() error {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println(version)
		return nil
	}
	if runtime.GOOS == "windows" && len(os.Args) == 1 && term.IsTerminal(int(os.Stdin.Fd())) {
		return setup(nil)
	}
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		return setup(os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] == "update" {
		return update(os.Args[2:])
	}
	if len(os.Args) > 1 && os.Args[1] == "uninstall" {
		return uninstall(os.Args[2:])
	}
	defaultToken, defaultData, err := userpaths.Defaults()
	if err != nil {
		if os.Getenv("AULAGLOBAL_DOWNLOAD_DIR") == "" {
			return err
		}
	}
	tokenFile := os.Getenv("AULAGLOBAL_TOKEN_FILE")
	if tokenFile == "" && os.Getenv("AULAGLOBAL_TOKEN") == "" {
		if _, e := os.Stat(defaultToken); e == nil {
			tokenFile = defaultToken
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	api := aulaglobal.New(aulaglobal.Site, aulaglobal.StaticToken{Value: os.Getenv("AULAGLOBAL_TOKEN"), File: tokenFile})
	if len(os.Args) > 1 && os.Args[1] == "token-from-cookie" {
		flags := flag.NewFlagSet("token-from-cookie", flag.ContinueOnError)
		force := flags.Bool("force", false, "Atomically replace existing token files")
		output := flags.String("output", "secrets/token", "New token file in private directory")
		if e := flags.Parse(os.Args[2:]); e != nil {
			return e
		}
		// Refuse a terminal to prevent echo and accidental paste into shell history.
		_, e := os.Stdin.Stat()
		if e != nil {
			return e
		}
		if term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("pipe cookie on stdin using a no-echo shell read; see README")
		}
		b, e := io.ReadAll(io.LimitReader(os.Stdin, 8193))
		if e != nil || len(b) > 8192 {
			return errors.New("cookie input too large or unreadable")
		}
		token, private, e := api.TokenFromCookie(ctx, string(b))
		if e != nil {
			return e
		}
		if e = aulaglobal.SaveSecret(*output, token, *force); e != nil {
			return e
		}
		if private != "" {
			if e = aulaglobal.SaveSecret(*output+".private", private, *force); e != nil {
				return e
			}
		}
		meta, _ := json.Marshal(map[string]string{"obtained_at": time.Now().UTC().Format(time.RFC3339)})
		if e = aulaglobal.SaveSecret(*output+".meta.json", string(meta), *force); e != nil {
			return e
		}
		slog.Info("token saved (private token stored separately when supplied)")
		return nil
	}
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
	max, e := number("AULAGLOBAL_MAX_FILE_BYTES", 256<<20)
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
	st, e := store.Open(env("AULAGLOBAL_DOWNLOAD_DIR", defaultData), max, int(workers))
	if e != nil {
		return e
	}
	defer st.Root.Close()
	server := mcpserver.New(api, st, startUpdateCheck(ctx))
	switch *transport {
	case "stdio":
		return server.Run(ctx, &mcp.StdioTransport{})
	case "http":
		token := strings.TrimSpace(os.Getenv("MCP_HTTP_AUTH_TOKEN"))
		if f := os.Getenv("MCP_HTTP_AUTH_TOKEN_FILE"); f != "" {
			b, e := os.ReadFile(f)
			if e != nil {
				return errors.New("cannot read HTTP auth token file")
			}
			token = strings.TrimSpace(string(b))
		}
		if !*unsafe && token == "" {
			return errors.New("HTTP requires MCP_HTTP_AUTH_TOKEN or MCP_HTTP_AUTH_TOKEN_FILE")
		}
		if *unsafe {
			slog.Warn("DANGER: HTTP authentication disabled; clients can access all downloaded course files")
		}
		_, port, e := net.SplitHostPort(*addr)
		if e != nil {
			return e
		}
		allowed := []string{net.JoinHostPort("localhost", port), net.JoinHostPort("127.0.0.1", port)}
		if *hosts != "" {
			allowed = strings.Split(*hosts, ",")
		}
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
		mux := http.NewServeMux()
		mux.Handle("/mcp", mcpserver.SecureHTTP(handler, token, *unsafe, allowed, strings.Split(*origins, ",")))
		// Go 1.26 clears ReadTimeout after body EOF before its background read;
		// this bounds body reads without imposing a tool execution deadline.
		httpServer := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
		done := make(chan error, 1)
		go func() { done <- httpServer.ListenAndServe() }()
		select {
		case err := <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ctx.Done():
			shutdown, c := context.WithTimeout(context.Background(), 10*time.Second)
			defer c()
			return httpServer.Shutdown(shutdown)
		}
	default:
		return errors.New("transport must be stdio or http")
	}
}
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	selfupdate.CleanupOld()
	if e := run(); e != nil {
		slog.Error("server stopped", "error", aulaglobal.Redact(e.Error(), os.Getenv("AULAGLOBAL_TOKEN"), os.Getenv("MCP_HTTP_AUTH_TOKEN")))
		os.Exit(1)
	}
}
