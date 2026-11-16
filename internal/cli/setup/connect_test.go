package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestPrivacyNotice(t *testing.T) {
	lines := strings.Split(strings.TrimRight(privacyNotice, "\n"), "\n")
	if len(lines) < 2 || len(lines) > 7 {
		t.Fatalf("notice must stay at 2-7 lines, has %d", len(lines))
	}
	for _, l := range lines {
		if n := len([]rune(l)); n > 80 {
			t.Errorf("line of %d columns: %q", n, l)
		}
	}
	for _, want := range []string{"Privacidad", "AulaGlobal", "el autor no recibe nada", "empresa del asistente", "otros archivos de tu ordenador"} {
		if !strings.Contains(privacyNotice, want) {
			t.Errorf("notice lacks %q", want)
		}
	}
	if !strings.HasPrefix(privacyNotice, "· ") {
		t.Error("notice must use the neutral · marker")
	}
}
