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
	for _, assistant := range assistants.All() {
		t.Run(assistant.ID(), func(t *testing.T) {
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
					env, entry, state, fail := assistantstest.Fixture(t, assistant, tc.present)
					*state = tc.state
					if tc.present && (tc.state == "different" || tc.state == "identical") {
						assistantstest.Seed(t, assistant, env, entry, tc.state)
					}
					if tc.fail {
						*fail = true
						env.WriteConfig = func(string, []byte) error { return errors.New("write failure") }
					}
					var out strings.Builder
					configureClients(termui.UI{In: strings.NewReader(tc.input), Out: &out}, env, entry, []assistants.Assistant{assistant}, tc.yes)
					output := out.String()
					if !strings.Contains(output, tc.want) {
						t.Fatal(output)
					}
					if tc.fail && (!strings.Contains(output, "Alternativa manual") || strings.Contains(output, "private diagnostic")) {
						t.Fatal(output)
					}
					if tc.state == "identical" && strings.Contains(output, "›") {
						t.Fatal("prompt for identical", output)
					}
					if tc.yes && strings.Contains(output, "›") {
						t.Fatal("prompt with --yes", output)
					}
					if (tc.want == "actualizado" || tc.want == "añadido") && assistant.Inspect(env, entry).State != assistants.Identical {
						t.Fatal("not identical after apply", assistant.Inspect(env, entry))
					}
				})
			}
		})
	}
}

func TestUnsafeFilesAndBackups(t *testing.T) {
	for _, assistant := range assistants.All() {
		if assistant.ID() == "claude-code" {
			continue
		}
		for _, bad := range []string{"{ // private\n}", `{"x":1,"x":2}`, `null`, `[]`, `{"mcpServers":null}`, `{"mcp":null}`} {
			if assistant.ID() == "codex" {
				bad = "[broken"
			}
			t.Run(assistant.ID()+bad, func(t *testing.T) {
				env, entry, _, _ := assistantstest.Fixture(t, assistant, true)
				configPath := assistantstest.ConfigPath(assistant, env)
				// Skip shapes that are valid unrelated keys for this particular client.
				if assistant.ID() == "opencode" && bad == `{"mcpServers":null}` {
					return
				}
				if assistant.ID() != "opencode" && bad == `{"mcp":null}` {
					return
				}
				os.WriteFile(configPath, []byte(bad), 0600)
				var out strings.Builder
				configureClients(termui.UI{In: strings.NewReader(""), Out: &out}, env, entry, []assistants.Assistant{assistant}, true)
				after, _ := os.ReadFile(configPath)
				if string(after) != bad || !strings.Contains(out.String(), "✗") {
					t.Fatal(out.String(), string(after))
				}
				if strings.Contains(out.String(), "// private") {
					t.Fatal("private content leaked")
				}
			})
		}
		t.Run(assistant.ID()+" backup", func(t *testing.T) {
			env, entry, _, _ := assistantstest.Fixture(t, assistant, true)
			assistantstest.Seed(t, assistant, env, entry, "different")
			configPath := assistantstest.ConfigPath(assistant, env)
			before, _ := os.ReadFile(configPath)
			if err := assistant.Apply(env, entry, true); err != nil {
				t.Fatal(err)
			}
			backup, err := os.ReadFile(configPath + ".bak")
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
				assistant := assistantstest.ByID(t, "claude-code")
				env, entry, state, _ := assistantstest.Fixture(t, assistant, true)
				*state = scope
				var out strings.Builder
				configureClients(termui.UI{In: strings.NewReader(answer), Out: &out}, env, entry, []assistants.Assistant{assistant}, false)
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
		assistant := assistantstest.ByID(t, "claude-code")
		env, entry, _, _ := assistantstest.Fixture(t, assistant, true)
		calls := 0
		env.RunClaude = func(context.Context, string, ...string) ([]byte, error) { calls++; return []byte(tc.output), tc.err }
		var out strings.Builder
		configureClients(termui.UI{In: strings.NewReader(""), Out: &out}, env, entry, []assistants.Assistant{assistant}, true)
		if calls != 1 || !strings.Contains(out.String(), "✗") || strings.Contains(out.String(), "private diagnostic") {
			t.Fatal(out.String(), calls)
		}
	}
}

func TestAntigravityGeneratorFailure(t *testing.T) {
	assistant := assistantstest.ByID(t, "antigravity")
	env, entry, _, _ := assistantstest.Fixture(t, assistant, true)
	env.LookPath = func(n string) (string, error) {
		if n == "agy" {
			return "fake-agy", nil
		}
		return "", os.ErrNotExist
	}
	env.GenerateAntigravity = func(string, string, []string) (map[string]any, error) { return nil, errors.New("private diagnostic") }
	var out strings.Builder
	configureClients(termui.UI{In: strings.NewReader(""), Out: &out}, env, entry, []assistants.Assistant{assistant}, true)
	if !strings.Contains(out.String(), "✗") || strings.Contains(out.String(), "private diagnostic") {
		t.Fatal(out.String())
	}
	configPath := assistantstest.ConfigPath(assistant, env)
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatal("written after generator failure")
	}
}

func TestOpenCodeJSONCIsLeftAlone(t *testing.T) {
	env, entry, _, _ := assistantstest.Fixture(t, assistantstest.ByID(t, "opencode"), true)
	dir := userpaths.OpenCode(env.Home, env.XDGConfigHome)
	path := filepath.Join(dir, "opencode.jsonc")
	old := []byte("{ // keep\n}")
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	configureClients(termui.UI{In: strings.NewReader("\n"), Out: &out}, env, entry, []assistants.Assistant{assistantstest.ByID(t, "opencode")}, false)
	b, _ := os.ReadFile(path)
	if string(b) != string(old) || !strings.Contains(out.String(), "Alternativa manual") {
		t.Fatal(out.String())
	}
}
