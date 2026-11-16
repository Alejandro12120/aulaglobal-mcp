package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/assistants/assistantstest"
	"aulaglobal-mcp/internal/termui"
	"aulaglobal-mcp/internal/userpaths"
)

func TestAssistantContracts(t *testing.T) {
	for _, a := range assistants.All() {
		t.Run(a.ID(), func(t *testing.T) {
			for _, tc := range []struct {
				name, state, input, want string
				present, yes, fail       bool
			}{
				{name: "not detected", want: "No encontrados:", present: false},
				{"missing add", "missing", "\n", "añadido", true, false, false},
				{"missing skip", "missing", "n\n", "omitido", true, false, false},
				{"identical", "identical", "", "ya estaba", true, false, false},
				{"different update", "different", "\n", "actualizado", true, false, false},
				{"different skip", "different", "n\n", "omitido", true, false, false},
				{"yes", "different", "", "actualizado", true, true, false},
				{"failure", "missing", "\n", "✗", true, false, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					e, w, state, fail := assistantstest.Fixture(t, a, tc.present)
					*state = tc.state
					if tc.present && (tc.state == "different" || tc.state == "identical") {
						assistantstest.Seed(t, a, e, w, tc.state)
					}
					if tc.fail {
						*fail = true
						e.WriteConfig = func(string, []byte) error { return errors.New("write failure") }
					}
					var out strings.Builder
					configureClients(termui.UI{In: strings.NewReader(tc.input), Out: &out}, e, w, []assistants.Assistant{a}, tc.yes)
					s := out.String()
					if !strings.Contains(s, tc.want) {
						t.Fatal(s)
					}
					if tc.fail && (!strings.Contains(s, "Alternativa manual") || strings.Contains(s, "private diagnostic")) {
						t.Fatal(s)
					}
					if tc.state == "identical" && strings.Contains(s, "›") {
						t.Fatal("prompt for identical", s)
					}
					if tc.yes && strings.Contains(s, "›") {
						t.Fatal("prompt with --yes", s)
					}
					if (tc.want == "actualizado" || tc.want == "añadido") && a.Inspect(e, w).State != assistants.Identical {
						t.Fatal("not identical after apply", a.Inspect(e, w))
					}
				})
			}
		})
	}
}

func TestUnsafeFilesAndBackups(t *testing.T) {
	for _, a := range assistants.All() {
		if a.ID() == "claude-code" {
			continue
		}
		for _, bad := range []string{"{ // private\n}", `{"x":1,"x":2}`, `null`, `[]`, `{"mcpServers":null}`, `{"mcp":null}`} {
			if a.ID() == "codex" {
				bad = "[broken"
			}
			t.Run(a.ID()+bad, func(t *testing.T) {
				e, w, _, _ := assistantstest.Fixture(t, a, true)
				p := assistantstest.ConfigPath(a, e)
				// Skip shapes that are valid unrelated keys for this particular client.
				if a.ID() == "opencode" && bad == `{"mcpServers":null}` {
					return
				}
				if a.ID() != "opencode" && bad == `{"mcp":null}` {
					return
				}
				os.WriteFile(p, []byte(bad), 0600)
				var out strings.Builder
				configureClients(termui.UI{In: strings.NewReader(""), Out: &out}, e, w, []assistants.Assistant{a}, true)
				after, _ := os.ReadFile(p)
				if string(after) != bad || !strings.Contains(out.String(), "✗") {
					t.Fatal(out.String(), string(after))
				}
				if strings.Contains(out.String(), "// private") {
					t.Fatal("private content leaked")
				}
			})
		}
		t.Run(a.ID()+" backup", func(t *testing.T) {
			e, w, _, _ := assistantstest.Fixture(t, a, true)
			assistantstest.Seed(t, a, e, w, "different")
			p := assistantstest.ConfigPath(a, e)
			before, _ := os.ReadFile(p)
			if err := a.Apply(e, w, true); err != nil {
				t.Fatal(err)
			}
			backup, err := os.ReadFile(p + ".bak")
			if err != nil || string(backup) != string(before) {
				t.Fatal("backup mismatch", err)
			}
		})
	}
}

func TestOtherClaudeScopes(t *testing.T) {
	for _, scope := range []string{"local", "project"} {
		for _, answer := range []string{"\n", "n\n"} {
			t.Run(scope+answer, func(t *testing.T) {
				a := assistantstest.ByID(t, "claude-code")
				e, w, state, _ := assistantstest.Fixture(t, a, true)
				*state = scope
				var out strings.Builder
				configureClients(termui.UI{In: strings.NewReader(answer), Out: &out}, e, w, []assistants.Assistant{a}, false)
				want := "actualizado"
				if answer == "n\n" {
					want = "omitido"
					if *state != scope {
						t.Fatal("changed after decline")
					}
				}
				if !strings.Contains(out.String(), want) {
					t.Fatal(out.String())
				}
			})
		}
	}
}

func TestClaudeUnknownInspection(t *testing.T) {
	for _, tc := range []struct {
		output string
		err    error
	}{{"Scope: Unknown config", nil}, {"private diagnostic", errors.New("offline")}} {
		a := assistantstest.ByID(t, "claude-code")
		e, w, _, _ := assistantstest.Fixture(t, a, true)
		calls := 0
		e.RunClaude = func(context.Context, string, ...string) ([]byte, error) { calls++; return []byte(tc.output), tc.err }
		var out strings.Builder
		configureClients(termui.UI{In: strings.NewReader(""), Out: &out}, e, w, []assistants.Assistant{a}, true)
		if calls != 1 || !strings.Contains(out.String(), "✗") || strings.Contains(out.String(), "private diagnostic") {
			t.Fatal(out.String(), calls)
		}
	}
}

func TestAntigravityGeneratorFailure(t *testing.T) {
	a := assistantstest.ByID(t, "antigravity")
	e, w, _, _ := assistantstest.Fixture(t, a, true)
	e.LookPath = func(n string) (string, error) {
		if n == "agy" {
			return "fake-agy", nil
		}
		return "", os.ErrNotExist
	}
	e.GenerateAntigravity = func(string, string, []string) (map[string]any, error) { return nil, errors.New("private diagnostic") }
	var out strings.Builder
	configureClients(termui.UI{In: strings.NewReader(""), Out: &out}, e, w, []assistants.Assistant{a}, true)
	if !strings.Contains(out.String(), "✗") || strings.Contains(out.String(), "private diagnostic") {
		t.Fatal(out.String())
	}
	p := assistantstest.ConfigPath(a, e)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("written after generator failure")
	}
}

func TestOpenCodeJSONCIsLeftAlone(t *testing.T) {
	e, w, _, _ := assistantstest.Fixture(t, assistantstest.ByID(t, "opencode"), true)
	dir := userpaths.OpenCode(e.Home, e.XDGConfigHome)
	path := filepath.Join(dir, "opencode.jsonc")
	old := []byte("{ // keep\n}")
	if e := os.WriteFile(path, old, 0600); e != nil {
		t.Fatal(e)
	}
	var out strings.Builder
	configureClients(termui.UI{In: strings.NewReader("\n"), Out: &out}, e, w, []assistants.Assistant{assistantstest.ByID(t, "opencode")}, false)
	b, _ := os.ReadFile(path)
	if string(b) != string(old) || !strings.Contains(out.String(), "Alternativa manual") {
		t.Fatal(out.String())
	}
}
