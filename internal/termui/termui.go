// Package termui asks the user questions in a terminal.
package termui

import (
	"fmt"
	"io"
	"strings"
)

// UI is the terminal that setup and uninstall talk to. Password reads a line
// without echo; it is only needed where the cookie is requested.
type UI struct {
	In       io.Reader
	Out      io.Writer
	Password func() ([]byte, error)
}

// Line reads one line of input. It reads a byte at a time because a buffered
// reader would swallow the input that Password reads later.
func (ui UI) Line() string {
	var one [1]byte
	var line strings.Builder
	for {
		n, err := ui.In.Read(one[:])
		if n > 0 {
			if one[0] == '\n' {
				break
			}
			if one[0] != '\r' {
				line.WriteByte(one[0])
			}
		}
		if err != nil {
			break
		}
	}
	return line.String()
}

// AcceptsYes reports whether an answer, already lower-cased and trimmed, means yes.
func AcceptsYes(answer string) bool { return answer == "s" || answer == "si" || answer == "sí" }

// Yes asks a question whose default answer is no.
func (ui UI) Yes(question string) bool {
	fmt.Fprint(ui.Out, "\n› "+question+" ")
	return AcceptsYes(strings.ToLower(strings.TrimSpace(ui.Line())))
}

// Confirm asks a question whose default answer is yes. With assume it asks
// nothing and answers yes.
func (ui UI) Confirm(question string, assume bool) bool {
	if assume {
		return true
	}
	fmt.Fprint(ui.Out, "› "+question+" [S/n] ")
	answer := strings.ToLower(strings.TrimSpace(ui.Line()))
	fmt.Fprintln(ui.Out)
	return answer == "" || AcceptsYes(answer)
}
