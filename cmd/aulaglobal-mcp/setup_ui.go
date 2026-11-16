package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"
)

// setupUI is the terminal that setup and uninstall talk to. password reads a
// line without echo and is only needed when setup asks for the cookie.
type setupUI struct {
	in       io.Reader
	out      io.Writer
	password func() ([]byte, error)
}

// line reads one line of input. It reads a byte at a time because a buffered
// reader would swallow the input that password reads later.
func (u setupUI) line() string {
	var b [1]byte
	var s strings.Builder
	for {
		n, e := u.in.Read(b[:])
		if n > 0 {
			if b[0] == '\n' {
				break
			}
			if b[0] != '\r' {
				s.WriteByte(b[0])
			}
		}
		if e != nil {
			break
		}
	}
	return s.String()
}

func acceptsYes(answer string) bool { return answer == "s" || answer == "si" || answer == "sí" }

// yes asks a question whose default answer is no.
func (u setupUI) yes(q string) bool {
	fmt.Fprint(u.out, "\n› "+q+" ")
	return acceptsYes(strings.ToLower(strings.TrimSpace(u.line())))
}

// confirm asks a question whose default answer is yes. With assume it asks
// nothing and answers yes.
func (u setupUI) confirm(q string, assume bool) bool {
	if assume {
		return true
	}
	fmt.Fprint(u.out, "› "+q+" [S/n] ")
	a := strings.ToLower(strings.TrimSpace(u.line()))
	fmt.Fprintln(u.out)
	return a == "" || acceptsYes(a)
}

// step prints the header of one of the four setup steps, in bold on a terminal.
func (u setupUI) step(n int, title string) {
	header := fmt.Sprintf("── Paso %d de 4 · %s ──", n, title)
	if f, ok := u.out.(*os.File); ok && runtime.GOOS != "windows" && term.IsTerminal(int(f.Fd())) {
		if _, disabled := os.LookupEnv("NO_COLOR"); !disabled {
			header = "\033[1m" + header + "\033[0m"
		}
	}
	fmt.Fprintf(u.out, "\n%s\n\n", header)
}
