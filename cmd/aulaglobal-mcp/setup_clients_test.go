package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"aulaglobal-mcp/internal/userpaths"
)

// Every registered implementation gets the same contract, with isolated files,
// fake executable metadata and subprocesses; no real HOME or service is used.
func clientFixture(t *testing.T, a assistant, present bool) (clientEnv, desiredEntry, *string, *bool) {
	t.Helper()
	home := t.TempDir()
	e := clientEnv{GOOS: runtime.GOOS, Home: home, Config: filepath.Join(home, "config")}
	e.Look = func(n string) (string, error) {
		if present && a.ID() == "claude-code" && n == "claude" {
			return "fake-claude", nil
		}
		return "", os.ErrNotExist
	}
	e.Stat = func(p string) (os.FileInfo, error) {
		if !strings.HasPrefix(p, home+string(os.PathSeparator)) {
			return nil, os.ErrNotExist
		}
		return os.Stat(p)
	}
	if present {
		var dir string
		switch a.ID() {
		case "claude-desktop":
			dir = filepath.Dir(userpaths.Claude(e.GOOS, e.Home, e.Config))
		case "codex":
			dir = filepath.Join(home, ".codex")
		case "opencode":
			dir = userpaths.OpenCode(home, "")
		case "antigravity":
			dir = filepath.Dir(userpaths.Antigravity(home))
		}
		if dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	w, err := desired(filepath.Join(home, "server"), filepath.Join(home, "private", "token"), filepath.Join(home, "notes"), "default-token", "default-data")
	if err != nil {
		t.Fatal(err)
	}
	state := "missing"
	fail := false
	e.Run = func(ctx context.Context, cli string, args ...string) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("no timeout")
		}
		if args[1] == "get" {
			if state == "missing" {
				return []byte("No MCP server named \"aulaglobal\". Run `claude mcp add` to add one."), errors.New("missing")
			}
			scope := "User config (available in all your projects)"
			if state == "local" {
				scope = "Local config (private to you in this project)"
			}
			if state == "project" {
				scope = "Project config (shared via .mcp.json)"
			}
			command := w.Exe
			if state != "identical" {
				command = "old-server"
			}
			return []byte(fmt.Sprintf("aulaglobal:\n  Scope: %s\n  Type: stdio\n  Command: %s\n  Args: --transport stdio\n  Environment:\n    AULAGLOBAL_TOKEN_FILE=%s\n    AULAGLOBAL_DOWNLOAD_DIR=%s\n\nTo remove this server, run: claude mcp remove aulaglobal -s user", scope, command, filepath.Join(home, "private", "token"), filepath.Join(home, "notes"))), nil
		}
		if fail {
			return []byte("private diagnostic"), errors.New("private error")
		}
		if args[1] == "remove" {
			if state == "project" {
				t.Fatal("project must not be removed")
			}
			if args[len(args)-1] != state && state != "different" {
				t.Fatal(args, state)
			}
			state = "missing"
		}
		if args[1] == "add" {
			state = "identical"
		}
		return nil, nil
	}
	e.OpenRun = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("unsupported") }
	e.GenerateAG = func(string, string, []string) (map[string]any, error) { return w.entry(), nil }
	return e, w, &state, &fail
}
func seedClient(t *testing.T, a assistant, e clientEnv, w desiredEntry, state string) {
	t.Helper()
	if a.ID() == "claude-code" {
		return
	}
	if state == "different" {
		w.Exe += "-old"
	}
	var p string
	var b []byte
	var err error
	switch a := a.(type) {
	case fileAssistant:
		p, err = a.path(e)
		if err == nil {
			b, _, err = a.merge(nil, w.entry(), true)
		}
	case openCodeAssistant:
		p, err = a.path(e)
		if err == nil {
			b, _, err = openCodeConfig(nil, openCodeEntry(w.Exe, w.Extra), true)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestAssistantContracts(t *testing.T) {
	for _, a := range assistants {
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
					e, w, state, fail := clientFixture(t, a, tc.present)
					*state = tc.state
					if tc.present && (tc.state == "different" || tc.state == "identical") {
						seedClient(t, a, e, w, tc.state)
					}
					if tc.fail {
						*fail = true
						e.Write = func(string, []byte) error { return errors.New("write failure") }
					}
					var out strings.Builder
					configureClients(setupUI{strings.NewReader(tc.input), &out, nil}, e, w, []assistant{a}, tc.yes)
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
					if (tc.want == "actualizado" || tc.want == "añadido") && a.Inspect(e, w).state != identical {
						t.Fatal("not identical after apply", a.Inspect(e, w))
					}
				})
			}
		})
	}
}
func TestClientSelection(t *testing.T) {
	for _, a := range assistants {
		got, err := selectAssistants(a.ID())
		if err != nil || len(got) != 1 || got[0].ID() != a.ID() {
			t.Fatal(got, err)
		}
	}
	if _, err := selectAssistants("bad"); err == nil {
		t.Fatal("invalid ID")
	}
	got, err := selectAssistants("codex,claude-code,codex")
	if err != nil || len(got) != 2 || got[0].ID() != "claude-code" {
		t.Fatal(got, err)
	}
}
func TestOtherClaudeScopes(t *testing.T) {
	for _, scope := range []string{"local", "project"} {
		for _, answer := range []string{"\n", "n\n"} {
			t.Run(scope+answer, func(t *testing.T) {
				a := claudeAssistant{}
				e, w, state, _ := clientFixture(t, a, true)
				*state = scope
				var out strings.Builder
				configureClients(setupUI{strings.NewReader(answer), &out, nil}, e, w, []assistant{a}, false)
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
func TestDesiredDefaults(t *testing.T) {
	w, err := desired("server", "token", "data", "token", "data")
	if err != nil || !filepath.IsAbs(w.Exe) || w.entry()["env"] != nil {
		t.Fatal(w, err)
	}
	w, err = desired("server", "private", "notes", "token", "data")
	if err != nil || len(w.entry()["env"].(map[string]string)) != 2 {
		t.Fatal(w, err)
	}
}
func TestNoHostMetadataForSimulatedOS(t *testing.T) {
	// Explicit fake directories also exercise Detect without host permission bits.
	for _, a := range assistants {
		e, _, _, _ := clientFixture(t, a, false)
		e.GOOS = "windows"
		e.Look = func(string) (string, error) { return "", os.ErrNotExist }
		e.Stat = func(string) (os.FileInfo, error) {
			return fstest.MapFS{"dir": &fstest.MapFile{Mode: os.ModeDir | 0600}}.Stat("dir")
		}
		if a.ID() != "claude-code" && !a.Detect(e) {
			t.Fatal(a.ID())
		}
	}
}

func TestClientTranscripts(t *testing.T) {
	for _, scenario := range []string{"local-and-codex", "configure-all", "already-configured", "failure"} {
		t.Run(scenario, func(t *testing.T) {
			a := claudeAssistant{}
			e, w, state, fail := clientFixture(t, a, true)
			// Stable desired command makes golden files independent of OS and temp path.
			w = desiredEntry{Exe: "/installed/aulaglobal-mcp"}
			input := "e\ns\nn\n"
			list := assistants
			switch scenario {
			case "local-and-codex":
				*state = "local"
				os.MkdirAll(filepath.Join(e.Home, ".codex"), 0700)
				seedClient(t, codexAssistant(), e, w, "different")
			case "configure-all":
				input = "\n"
				os.MkdirAll(filepath.Join(e.Home, ".codex"), 0700)
			case "already-configured":
				*state = "identical"
				list = []assistant{codexAssistant()}
				os.MkdirAll(filepath.Join(e.Home, ".codex"), 0700)
				seedClient(t, codexAssistant(), e, w, "identical")
			case "failure":
				*fail = true
				list = []assistant{a}
				input = "s\n"
			}
			var out strings.Builder
			connectAssistants(setupUI{strings.NewReader(input), &out, nil}, e, w, list, false)
			got := strings.ReplaceAll(out.String(), "\r\n", "\n")
			if !strings.Contains(got, "── Paso 4 de 4 · Conectar con tus asistentes ──\n\n"+privacyNotice+"\nAsistentes encontrados") {
				t.Fatalf("step 4 must show the privacy notice before the assistants:\n%s", got)
			}
			lines := strings.Split(got, "\n")
			for i := range lines {
				lines[i] = strings.TrimRight(lines[i], " \t")
			}
			got = strings.Join(lines, "\n")
			// Manual command quoting is shell-specific; failure golden checks the stable
			// prefix separately from the copy-paste command, which has dedicated tests.
			if scenario == "failure" {
				start := strings.Index(got, "── Alternativa manual")
				end := strings.Index(got, "\nResumen:")
				got = got[:start] + "Alternativa manual · Claude Code: <comando para este sistema>\n" + got[end:]
			}
			path := filepath.Join("testdata", "setup-"+scenario+".golden")
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.WriteFile(path, []byte(got), 0600); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if diff := transcriptDifference(string(want), got); diff != "" {
				t.Fatalf("transcript mismatch: %s\n%s", diff, got)
			}
		})
	}
}

// Quote the first differing line so invisible whitespace is visible in CI logs.
func transcriptDifference(want, got string) string {
	want = strings.ReplaceAll(want, "\r\n", "\n")
	got = strings.ReplaceAll(got, "\r\n", "\n")
	if want == got {
		return ""
	}
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		if i >= len(w) {
			return fmt.Sprintf("line %d: want <EOF>, got %q", i+1, g[i])
		}
		if i >= len(g) {
			return fmt.Sprintf("line %d: want %q, got <EOF>", i+1, w[i])
		}
		if w[i] != g[i] {
			return fmt.Sprintf("line %d: want %q, got %q", i+1, w[i], g[i])
		}
	}
	return ""
}

func TestTranscriptDifference(t *testing.T) {
	for _, tc := range []struct{ want, got, diff string }{
		{"one\r\ntwo\r\n", "one\ntwo\n", ""},
		{"one\ntwo\n", "one\r\ntwo\r\n", ""},
		{"one\ntwo\n", "one\ntwo \n", `line 2: want "two", got "two "`},
		{"one\n", "one", `line 2: want "", got <EOF>`},
		{"one", "one\n", `line 2: want <EOF>, got ""`},
	} {
		if got := transcriptDifference(tc.want, tc.got); got != tc.diff {
			t.Fatalf("difference = %q, want %q", got, tc.diff)
		}
	}
}

func TestUnsafeFilesAndBackups(t *testing.T) {
	for _, a := range assistants {
		if a.ID() == "claude-code" {
			continue
		}
		for _, bad := range []string{"{ // private\n}", `{"x":1,"x":2}`, `null`, `[]`, `{"mcpServers":null}`, `{"mcp":null}`} {
			if a.ID() == "codex" {
				bad = "[broken"
			}
			t.Run(a.ID()+bad, func(t *testing.T) {
				e, w, _, _ := clientFixture(t, a, true)
				var p string
				switch a := a.(type) {
				case fileAssistant:
					p, _ = a.path(e)
				case openCodeAssistant:
					p, _ = a.path(e)
				}
				// Skip shapes that are valid unrelated keys for this particular client.
				if a.ID() == "opencode" && bad == `{"mcpServers":null}` {
					return
				}
				if a.ID() != "opencode" && bad == `{"mcp":null}` {
					return
				}
				os.WriteFile(p, []byte(bad), 0600)
				var out strings.Builder
				configureClients(setupUI{strings.NewReader(""), &out, nil}, e, w, []assistant{a}, true)
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
			e, w, _, _ := clientFixture(t, a, true)
			seedClient(t, a, e, w, "different")
			var p string
			switch a := a.(type) {
			case fileAssistant:
				p, _ = a.path(e)
			case openCodeAssistant:
				p, _ = a.path(e)
			}
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
func TestOpenCodeCapabilities(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			for _, mode := range []string{"scriptable", "legacy", "failure", "unverified", "both files"} {
				t.Run(mode, func(t *testing.T) {
					a := openCodeAssistant{}
					e, w, _, _ := clientFixture(t, a, true)
					e.GOOS = goos
					p, _ := a.path(e)
					calls := 0
					e.Look = func(n string) (string, error) {
						if (goos == "windows" && n == "opencode.exe") || (goos != "windows" && n == "opencode") {
							return "fake-opencode", nil
						}
						return "", os.ErrNotExist
					}
					if mode == "both files" {
						os.WriteFile(p, []byte(`{}`), 0600)
						os.WriteFile(p+"c", []byte(`{}`), 0600)
					}
					e.OpenRun = func(ctx context.Context, cli string, args ...string) ([]byte, error) {
						if _, ok := ctx.Deadline(); !ok {
							t.Fatal("no deadline")
						}
						calls++
						if cli != "fake-opencode" {
							t.Fatalf("unexpected CLI %q", cli)
						}
						if args[len(args)-1] == "--help" {
							if mode == "legacy" {
								return os.ReadFile("testdata/opencode-add-help.txt")
							}
							return []byte("--global\r\n--env\r\n"), nil
						}
						if mode == "failure" {
							return nil, errors.New("private diagnostic")
						}
						if !sameEntry(args, openCodeArgs(w.Exe, w.Extra)) {
							t.Fatal(args)
						}
						if mode != "unverified" {
							b, _, _ := openCodeConfig(nil, openCodeEntry(w.Exe, w.Extra), true)
							os.WriteFile(p, b, 0600)
						}
						return nil, nil
					}
					var out strings.Builder
					configureClients(setupUI{strings.NewReader(""), &out, nil}, e, w, []assistant{a}, true)
					failure := mode == "failure" || mode == "unverified" || mode == "both files"
					if strings.Contains(out.String(), "✗") != failure {
						t.Fatal(out.String())
					}
					if mode == "both files" && calls != 0 {
						t.Fatal("called CLI despite conflicting files")
					}
					if mode != "both files" {
						wantCalls := 2 // Capability probe followed by registration.
						if mode == "legacy" {
							wantCalls = 1 // Unsupported CLI uses the config writer.
						}
						if calls != wantCalls {
							t.Fatalf("CLI calls = %d, want %d", calls, wantCalls)
						}
					}
					if mode == "failure" || mode == "unverified" {
						if _, err := os.Stat(p); !os.IsNotExist(err) {
							t.Fatalf("config fallback after CLI failure: %v", err)
						}
					}
				})
			}
		})
	}
}

func TestAntigravityGeneratorFailure(t *testing.T) {
	a := antigravityAssistant()
	e, w, _, _ := clientFixture(t, a, true)
	e.Look = func(n string) (string, error) {
		if n == "agy" {
			return "fake-agy", nil
		}
		return "", os.ErrNotExist
	}
	e.GenerateAG = func(string, string, []string) (map[string]any, error) { return nil, errors.New("private diagnostic") }
	var out strings.Builder
	configureClients(setupUI{strings.NewReader(""), &out, nil}, e, w, []assistant{a}, true)
	if !strings.Contains(out.String(), "✗") || strings.Contains(out.String(), "private diagnostic") {
		t.Fatal(out.String())
	}
	p := userpaths.Antigravity(e.Home)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("written after generator failure")
	}
}
func TestOpenCodeConflictsAndV1(t *testing.T) {
	a := openCodeAssistant{}
	e, w, _, _ := clientFixture(t, a, true)
	p, _ := a.path(e)
	before := []byte(`{"theme":"keep","mcp":{"other":{"type":"local","command":["keep"]},"aulaglobal":{"type":"local","command":["old"]}}}`)
	os.WriteFile(p, before, 0600)
	if err := a.Apply(e, w, true); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(p)
	if !strings.Contains(string(after), `"enabled": true`) || !strings.Contains(string(after), `"other"`) {
		t.Fatal(string(after))
	}
	backup, _ := os.ReadFile(p + ".bak")
	if string(backup) != string(before) {
		t.Fatal("backup changed")
	}
	if err := os.WriteFile(p+"c", []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(e, w, true); err == nil {
		t.Fatal("ambiguous files accepted")
	}
	unchanged, _ := os.ReadFile(p)
	if string(after) != string(unchanged) {
		t.Fatal("changed on conflict")
	}
}
func TestClientCLIArgumentsAndManual(t *testing.T) {
	want := []string{"mcp", "add", "--scope", "user", "aulaglobal", "-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes space", "--", "/server", "--transport", "stdio"}
	if !sameEntry(codeArgs("/server", []string{"-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes space"}), want) {
		t.Fatal("Claude variadic env consumed the name")
	}
	extra := []string{"-e", "SECRET=never", "-e", "AULAGLOBAL_TOKEN_FILE=/token"}
	if strings.Contains(strings.Join(openCodeArgs("/exe", extra), " "), "never") {
		t.Fatal("unapproved env")
	}
	var out strings.Builder
	manualCode(setupUI{out: &out}, "claude", "/path with spaces/server", []string{"-e", "AULAGLOBAL_DOWNLOAD_DIR=/notes"})
	for _, part := range []string{"'--scope' 'user'", "'/path with spaces/server'", "'AULAGLOBAL_DOWNLOAD_DIR=/notes'"} {
		if !strings.Contains(out.String(), part) {
			t.Fatal(out.String())
		}
	}
}

func TestSetupOnlyClientsFlags(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "APPDATA", "XDG_CONFIG_HOME"} {
		t.Setenv(k, home)
	}
	t.Setenv("AULAGLOBAL_TOKEN_FILE", "")
	t.Setenv("AULAGLOBAL_DOWNLOAD_DIR", "")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	// Redirect to a file, not a pipe: setup must run without a terminal and without
	// waiting for input. No token exists and no network can be needed in this mode.
	f, err := os.CreateTemp(home, "transcript")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stdout := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = stdout }()
	for i := 0; i < 2; i++ {
		if err := setup([]string{"--only-clients", "--clients", "codex", "--yes"}); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "añadido") || !strings.Contains(s, "ya está configurado") || strings.Contains(s, "Claude") || strings.Contains(s, "›") {
		t.Fatal(s)
	}
	if err := setup([]string{"--clients", "invalid"}); err == nil {
		t.Fatal("invalid flag accepted")
	}
}
func TestClaudeUnknownInspection(t *testing.T) {
	for _, tc := range []struct {
		output string
		err    error
	}{{"Scope: Unknown config", nil}, {"private diagnostic", errors.New("offline")}} {
		a := claudeAssistant{}
		e, w, _, _ := clientFixture(t, a, true)
		calls := 0
		e.Run = func(context.Context, string, ...string) ([]byte, error) { calls++; return []byte(tc.output), tc.err }
		var out strings.Builder
		configureClients(setupUI{strings.NewReader(""), &out, nil}, e, w, []assistant{a}, true)
		if calls != 1 || !strings.Contains(out.String(), "✗") || strings.Contains(out.String(), "private diagnostic") {
			t.Fatal(out.String(), calls)
		}
	}
}

func TestPrivacyNotice(t *testing.T) {
	lines := strings.Split(strings.TrimRight(privacyNotice, "\n"), "\n")
	if len(lines) < 2 || len(lines) > 4 {
		t.Fatalf("notice must stay at 2-4 lines, has %d", len(lines))
	}
	for _, l := range lines {
		if n := len([]rune(l)); n > 80 {
			t.Errorf("line of %d columns: %q", n, l)
		}
	}
	for _, want := range []string{"Privacidad", "AulaGlobal", "asistente", "proveedor de IA", "restricciones de carpeta"} {
		if !strings.Contains(privacyNotice, want) {
			t.Errorf("notice lacks %q", want)
		}
	}
	if !strings.HasPrefix(privacyNotice, "· ") {
		t.Error("notice must use the neutral · marker")
	}
}
