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
	if err := flags.Parse(args); err != nil {
		return err
	}
	// A terminal would echo the cookie and could put it in the shell history.
	if _, err := os.Stdin.Stat(); err != nil {
		return err
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("pipe cookie on stdin using a no-echo shell read; see README")
	}
	cookie, err := io.ReadAll(io.LimitReader(os.Stdin, 8193))
	if err != nil || len(cookie) > 8192 {
		return errors.New("cookie input too large or unreadable")
	}
	tokenValue, privateToken, err := api.TokenFromCookie(ctx, string(cookie))
	if err != nil {
		return err
	}
	if err = aulaglobal.SaveSecret(*output, tokenValue, *force); err != nil {
		return err
	}
	if privateToken != "" {
		if err = aulaglobal.SaveSecret(*output+".private", privateToken, *force); err != nil {
			return err
		}
	}
	if err = saveTokenMeta(*output, *force); err != nil {
		return err
	}
	slog.Info("token saved (private token stored separately when supplied)")
	return nil
}
