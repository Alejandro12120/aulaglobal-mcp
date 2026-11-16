package termui

import (
	"strings"
	"testing"
)

func TestLine(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"hola\n", "hola"},
		{"hola\r\n", "hola"},
		{"hola", "hola"},
		{"\n", ""},
		{"", ""},
	} {
		ui := UI{In: strings.NewReader(tc.input)}
		if got := ui.Line(); got != tc.want {
			t.Errorf("Line(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// Line must not read past the end of the line, because the next read may be
// the hidden password prompt on the same input.
func TestLineLeavesTheRestOfTheInput(t *testing.T) {
	input := strings.NewReader("first\nsecond\n")
	ui := UI{In: input}
	if got := ui.Line(); got != "first" {
		t.Fatalf("first line = %q", got)
	}
	if got := ui.Line(); got != "second" {
		t.Fatalf("second line = %q", got)
	}
}

func TestYesDefaultsToNo(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  bool
	}{
		{"s\n", true}, {"S\n", true}, {"si\n", true}, {"sí\n", true}, {" SÍ \n", true},
		{"\n", false}, {"n\n", false}, {"y\n", false}, {"", false},
	} {
		var out strings.Builder
		ui := UI{In: strings.NewReader(tc.input), Out: &out}
		if got := ui.Yes("¿Seguro?"); got != tc.want {
			t.Errorf("Yes with %q = %v, want %v", tc.input, got, tc.want)
		}
		if !strings.Contains(out.String(), "¿Seguro?") {
			t.Errorf("question not shown: %q", out.String())
		}
	}
}

func TestConfirmDefaultsToYes(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  bool
	}{
		{"\n", true}, {"s\n", true}, {"sí\n", true}, {"n\n", false}, {"no\n", false},
	} {
		var out strings.Builder
		ui := UI{In: strings.NewReader(tc.input), Out: &out}
		if got := ui.Confirm("¿Añadir?", false); got != tc.want {
			t.Errorf("Confirm with %q = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestConfirmWithAssumeAsksNothing(t *testing.T) {
	var out strings.Builder
	ui := UI{In: strings.NewReader(""), Out: &out}
	if !ui.Confirm("¿Añadir?", true) || out.Len() != 0 {
		t.Fatalf("assume must answer yes silently, printed %q", out.String())
	}
}
