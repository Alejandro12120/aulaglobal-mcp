package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/assistants/assistantstest"
	"aulaglobal-mcp/internal/termui"
	"aulaglobal-mcp/internal/testutil"
)

func TestClientTranscripts(t *testing.T) {
	for _, scenario := range []string{"local-and-codex", "configure-all", "already-configured", "failure"} {
		t.Run(scenario, func(t *testing.T) {
			a := assistantstest.ByID(t, "claude-code")
			env, entry, state, fail := assistantstest.Fixture(t, a, true)
			// Stable desired command makes golden files independent of OS and temp path.
			entry = assistants.Entry{Exe: "/installed/aulaglobal-mcp"}
			input := "e\ns\nn\n"
			list := assistants.All()
			switch scenario {
			case "local-and-codex":
				*state = "local"
				os.MkdirAll(filepath.Join(env.Home, ".codex"), 0700)
				assistantstest.Seed(t, assistantstest.ByID(t, "codex"), env, entry, "different")
			case "configure-all":
				input = "\n"
				os.MkdirAll(filepath.Join(env.Home, ".codex"), 0700)
			case "already-configured":
				*state = "identical"
				list = []assistants.Assistant{assistantstest.ByID(t, "codex")}
				os.MkdirAll(filepath.Join(env.Home, ".codex"), 0700)
				assistantstest.Seed(t, assistantstest.ByID(t, "codex"), env, entry, "identical")
			case "failure":
				*fail = true
				list = []assistants.Assistant{a}
				input = "s\n"
			}
			var out strings.Builder
			connectAssistants(termui.UI{In: strings.NewReader(input), Out: &out}, env, entry, list, false)
			got := strings.ReplaceAll(out.String(), "\r\n", "\n")
			if !strings.Contains(got, "── Paso 4 de 4 · Conectar con tus asistentes ──\n\n"+privacyNotice+"\nAsistentes encontrados") {
				t.Fatalf("step 4 must show the privacy notice before the assistants:\n%s", got)
			}
			lines := strings.Split(got, "\n")
			for index := range lines {
				lines[index] = strings.TrimRight(lines[index], " \t")
			}
			got = strings.Join(lines, "\n")
			// Manual command quoting is shell-specific; failure golden checks the stable
			// prefix separately from the copy-paste command, which has dedicated tests.
			if scenario == "failure" {
				start := strings.Index(got, "── Alternativa manual")
				end := strings.Index(got, "\nResumen:")
				got = got[:start] + "Alternativa manual · Claude Code: <comando para este sistema>\n" + got[end:]
			}
			testutil.Golden(t, "setup-"+scenario, got)
		})
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
	info, err := os.CreateTemp(home, "transcript")
	if err != nil {
		t.Fatal(err)
	}
	defer info.Close()
	stdout := os.Stdout
	os.Stdout = info
	defer func() { os.Stdout = stdout }()
	for index := 0; index < 2; index++ {
		if err := Run([]string{"--only-clients", "--clients", "codex", "--yes"}); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(info.Name())
	if err != nil {
		t.Fatal(err)
	}
	output := string(b)
	if !strings.Contains(output, "añadido") || !strings.Contains(output, "ya está configurado") || strings.Contains(output, "Claude") || strings.Contains(output, "›") {
		t.Fatal(output)
	}
	if err := Run([]string{"--clients", "invalid"}); err == nil {
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
