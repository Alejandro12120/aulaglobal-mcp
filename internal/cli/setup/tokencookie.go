package setup

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"

	"aulaglobal-mcp/internal/aulaglobal"
	"golang.org/x/term"
)

// tokenFromCookie is `aulaglobal-mcp token-from-cookie`: it reads the
// MoodleSessionag cookie from stdin, exchanges it for a token and saves it.
// `setup` does the same interactively.
func TokenFromCookie(ctx context.Context, api *aulaglobal.Client, args []string) error {
	flags := flag.NewFlagSet("token-from-cookie", flag.ContinueOnError)
	force := flags.Bool("force", false, "Atomically replace existing token files")
	output := flags.String("output", "secrets/token", "New token file in private directory")
	if e := flags.Parse(args); e != nil {
		return e
	}
	// A terminal would echo the cookie and could put it in the shell history.
	if _, e := os.Stdin.Stat(); e != nil {
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
	if e = saveTokenMeta(*output, *force); e != nil {
		return e
	}
	slog.Info("token saved (private token stored separately when supplied)")
	return nil
}
