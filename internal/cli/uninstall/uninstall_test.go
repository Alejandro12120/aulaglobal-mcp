package uninstall

import (
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/assistants/assistantstest"
	"aulaglobal-mcp/internal/termui"
	"aulaglobal-mcp/internal/testutil"
	"aulaglobal-mcp/internal/userpaths"
)

// memPath replaces the Windows registry in tests.
type memPath struct {
	value          string
	getErr, setErr error
	sets           int
}

func (m *memPath) Get() (string, error) { return m.value, m.getErr }
func (m *memPath) Set(v string) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.value = v
	m.sets++
	return nil
}

// ufix is a complete fake installation inside a temporary directory. Nothing in
// it refers to the real HOME, PATH, registry, executable or assistants.
type fakeInstall struct {
	t                      *testing.T
	root, home, installDir string
	exe, token, configDir  string
	cacheDir, data         string
	e                      uninstallEnv
	claude                 *assistantstest.FakeClaude
	reg                    *memPath
	spawned                []string
	zshrc, bashrc          string
	codexPath, desktopPath string
	pathLine               string
}

func newFakeInstall(t *testing.T) *fakeInstall {
	t.Helper()
	root := t.TempDir()
	install := &fakeInstall{t: t, root: root, home: filepath.Join(root, "home")}
	install.installDir = filepath.Join(root, "Programs", "aulaglobal-mcp")
	install.exe = filepath.Join(install.installDir, "aulaglobal-mcp")
	config := filepath.Join(install.home, "config")
	install.configDir = filepath.Join(config, "aulaglobal-mcp")
	install.cacheDir = filepath.Join(install.home, "cache", "aulaglobal-mcp")
	install.token = filepath.Join(install.configDir, "token")
	install.data = filepath.Join(install.home, "AulaGlobal")
	testutil.WriteFile(t, install.exe, "binary")
	for _, name := range []string{"token", "token.private", "token.meta.json"} {
		testutil.WriteFile(t, filepath.Join(install.configDir, name), "secret-"+name)
	}
	testutil.WriteFile(t, filepath.Join(install.cacheDir, "update-check.json"), "{}")
	testutil.WriteFile(t, filepath.Join(install.data, dataMarker, "manifest.json"), "{}")
	testutil.WriteFile(t, filepath.Join(install.data, "Fisica", "tema1.pdf"), "pdf")
	install.pathLine = `export PATH="` + install.installDir + `:$PATH" ` + pathMarker
	install.zshrc = filepath.Join(install.home, ".zshrc")
	install.bashrc = filepath.Join(install.home, ".bashrc")
	testutil.WriteFile(t, install.zshrc, "alias ll='ls -l'\n\n"+install.pathLine+"\n")
	testutil.WriteFile(t, install.bashrc, "# bashrc\n\n"+install.pathLine+"\n")
	testutil.WriteFile(t, filepath.Join(install.home, "canary.txt"), "mine")

	install.claude = &assistantstest.FakeClaude{Scope: "user", Command: install.exe}
	assistantEnv := assistants.Env{GOOS: runtime.GOOS, Home: install.home, ConfigDir: config}
	assistantEnv.LookPath = func(name string) (string, error) {
		if name == "claude" {
			return "fake-claude", nil
		}
		return "", os.ErrNotExist
	}
	assistantEnv.Stat = func(path string) (os.FileInfo, error) {
		if !strings.HasPrefix(path, install.home+string(os.PathSeparator)) {
			return nil, os.ErrNotExist
		}
		return os.Stat(path)
	}
	assistantEnv.RunClaude = install.claude.Run
	install.codexPath = filepath.Join(install.home, ".codex", "config.toml")
	testutil.WriteFile(t, install.codexPath, "model = \"x\"\n\n[mcp_servers.other]\ncommand = \"keep\"\n\n[mcp_servers.aulaglobal]\ncommand = '"+install.exe+"'\nargs = ['--transport', 'stdio']\n")
	install.reg = &memPath{}
	install.e = uninstallEnv{
		assistantEnv: assistantEnv, assistantList: []assistants.Assistant{assistantstest.ByID(t, "claude-code"), assistantstest.ByID(t, "codex"), assistantstest.ByID(t, "claude-desktop")}, goos: "linux", home: install.home,
		exe: install.exe, tokenFile: install.token, configDir: install.configDir, cacheDir: install.cacheDir, dataDefault: install.data,
		interactive: true, getenv: func(string) string { return "" }, removeBinary: removeBinaryUnix,
	}
	return install
}

func (install *fakeInstall) desktop(content string) {
	install.desktopPath = userpaths.Claude(install.e.assistantEnv.GOOS, install.home, install.e.assistantEnv.ConfigDir)
	testutil.WriteFile(install.t, install.desktopPath, content)
}

func (install *fakeInstall) run(opts uninstallOpts, input string) (string, error) {
	var out strings.Builder
	err := runUninstall(termui.UI{In: strings.NewReader(input), Out: &out}, install.e, opts)
	return out.String(), err
}

// snapshot hashes every file and directory under root.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	hashes := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if entry.IsDir() {
			hashes[rel] = "dir"
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hashes[rel] = fmt.Sprintf("%x", sha256.Sum256(content))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hashes
}
func sameSnapshot(before, after map[string]string) string {
	var diff []string
	for name, hash := range before {
		if after[name] != hash {
			diff = append(diff, name)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			diff = append(diff, name)
		}
	}
	sort.Strings(diff)
	return strings.Join(diff, ", ")
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func (install *fakeInstall) transcript(out string) string {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	out = strings.ReplaceAll(out, install.root, "<tmp>")
	out = strings.ReplaceAll(out, `\`, "/")
	lines := strings.Split(out, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.Join(lines, "\n")
}

// ---- flags ---------------------------------------------------------------------

func TestUninstallFlagValidation(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string // "" = valid
	}{
		{nil, ""},
		{[]string{"--all", "--yes", "--delete-data", "--dry-run"}, ""},
		{[]string{"--logout", "--secrets-dir", "/x"}, ""},
		{[]string{"--clients", "claude-code,codex"}, ""},
		{[]string{"--all", "--logout"}, "solo una"},
		{[]string{"--clients", "codex", "--all"}, "solo una"},
		{[]string{"--clients", "codex", "--logout"}, "solo una"},
		{[]string{"--delete-data"}, "--delete-data solo"},
		{[]string{"--logout", "--delete-data"}, "--delete-data solo"},
		{[]string{"--clients", "codex", "--delete-data"}, "--delete-data solo"},
		{[]string{"--clients", ""}, "al menos un asistente"},
		{[]string{"--clients", "nope"}, "Cliente desconocido"},
		{[]string{"--bogus"}, "bogus"},
		{[]string{"extra"}, "argumento inesperado"},
	} {
		_, err := parseUninstallFlags(tc.args)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%v: %v", tc.args, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%v: %v", tc.args, err)
		}
	}
	if _, err := parseUninstallFlags([]string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help: %v", err)
	}
	opts, _ := parseUninstallFlags([]string{"--yes", "--all"})
	if opts.deleteData {
		t.Fatal("--yes implied --delete-data")
	}
}

func TestUninstallHelpExitsCleanly(t *testing.T) {
	install, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer install.Close()
	old := os.Stdout
	os.Stdout = install
	defer func() { os.Stdout = old }()
	for _, a := range []string{"--help", "-h"} {
		if err := Run([]string{a}); err != nil {
			t.Fatal(err)
		}
	}
	os.Stdout = old
	content, _ := os.ReadFile(install.Name())
	for _, want := range []string{"Uso: aulaglobal-mcp uninstall", "--delete-data", "--dry-run", "claude-code,claude-desktop,codex,opencode,antigravity", "nunca implica --delete-data"} {
		if !strings.Contains(string(content), want) {
			t.Errorf("help lacks %q", want)
		}
	}
	if err := Run([]string{"--delete-data"}); err == nil || !strings.Contains(err.Error(), "Uso:") {
		t.Fatalf("usage error lacks help: %v", err)
	}
}

func TestUninstallWithoutTerminalOrFlagsChangesNothing(t *testing.T) {
	install := newFakeInstall(t)
	install.e.interactive = false
	before := snapshot(t, install.root)
	out, err := install.run(uninstallOpts{}, "")
	if err == nil || !strings.Contains(out, "Uso: aulaglobal-mcp uninstall") {
		t.Fatalf("%v\n%s", err, out)
	}
	if difference := sameSnapshot(before, snapshot(t, install.root)); difference != "" || install.claude.Scope != "user" || len(install.claude.Calls) != 0 && install.claude.Calls[len(install.claude.Calls)-1][1] == "remove" {
		t.Fatal("changed:", difference)
	}
	// With flags but no way to confirm, it asks for --yes instead of guessing.
	if _, err = install.run(uninstallOpts{all: true}, ""); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("%v", err)
	}
	if difference := sameSnapshot(before, snapshot(t, install.root)); difference != "" {
		t.Fatal("changed:", difference)
	}
	// --dry-run needs no confirmation.
	if _, err = install.run(uninstallOpts{all: true, dryRun: true}, ""); err != nil {
		t.Fatal(err)
	}
}

// ---- goldens -------------------------------------------------------------------

func TestUninstallTranscripts(t *testing.T) {
	for _, scenario := range []string{"disconnect", "logout", "all", "dry-run", "nothing-to-do"} {
		t.Run(scenario, func(t *testing.T) {
			install := newFakeInstall(t)
			install.desktop(`{"mcpServers":{"aulaglobal":{"command":"/usr/bin/node","args":["server.js"]},"keep":{"command":"k"}}}`)
			opts := uninstallOpts{}
			input := ""
			switch scenario {
			case "disconnect":
				input = "\n\nn\ns\n" // menu 1, all, keep the foreign entry, continue
			case "logout":
				input = "2\ns\n"
			case "all":
				input = "3\n\nn\ns\n" // menu 3, foreign entry: Enter (no), data folder: n, continue
			case "dry-run":
				opts.all, opts.dryRun = true, true
				input = "n\ns\n" // foreign: no; data folder: yes (shown in the plan only)
			case "nothing-to-do":
				install.claude.Scope = ""
				os.Remove(install.codexPath)
				os.Remove(install.desktopPath)
				input = "\n"
			}
			out, err := install.run(opts, input)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			testutil.Golden(t, "uninstall-"+scenario, install.transcript(out))
		})
	}
}

func TestUninstallMenuInvalidInputCancels(t *testing.T) {
	install := newFakeInstall(t)
	before := snapshot(t, install.root)
	out, err := install.run(uninstallOpts{}, "x\ny\nz\n")
	if err != nil || !strings.Contains(out, "Escribe 1, 2 o 3") || !strings.Contains(out, "No se ha cambiado nada") {
		t.Fatalf("%v\n%s", err, out)
	}
	if difference := sameSnapshot(before, snapshot(t, install.root)); difference != "" {
		t.Fatal(difference)
	}
}

// ---- option 1 --------------------------------------------------------------------

func TestUninstallDisconnectSelectedClients(t *testing.T) {
	install := newFakeInstall(t)
	out, err := install.run(uninstallOpts{clients: "codex", clientsSet: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(testutil.ReadFile(t, install.codexPath), "aulaglobal") || !strings.Contains(testutil.ReadFile(t, install.codexPath), "[mcp_servers.other]") {
		t.Fatal(testutil.ReadFile(t, install.codexPath))
	}
	if install.claude.Scope != "user" {
		t.Fatal("Claude Code was not selected but was changed")
	}
	// Naming an assistant that has nothing is reported, not an error.
	out, err = install.run(uninstallOpts{clients: "codex,opencode", clientsSet: true, yes: true}, "")
	if err != nil || !strings.Contains(out, "Codex / ChatGPT: no tiene AulaGlobal conectado") {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, path := range []string{install.exe, install.token, install.data, install.zshrc} {
		if !exists(path) {
			t.Fatalf("%s removed by option 1", path)
		}
	}
}

func TestUninstallInteractiveSelectionByNumber(t *testing.T) {
	install := newFakeInstall(t)
	// Claude Code is 1 and Codex is 2: choose only Codex.
	out, err := install.run(uninstallOpts{}, "1\n2\ns\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if install.claude.Scope != "user" || strings.Contains(testutil.ReadFile(t, install.codexPath), "aulaglobal") {
		t.Fatalf("selection ignored\n%s", out)
	}
	// 0 means none; invalid answers are asked again.
	install = newFakeInstall(t)
	before := snapshot(t, install.root)
	out, err = install.run(uninstallOpts{}, "1\n9\n0\n")
	if err != nil || !strings.Contains(out, "No te he entendido") {
		t.Fatalf("%v\n%s", err, out)
	}
	if difference := sameSnapshot(before, snapshot(t, install.root)); difference != "" {
		t.Fatal(difference)
	}
}

func TestUninstallForeignEntriesNeedAnExplicitYes(t *testing.T) {
	foreign := `{"mcpServers":{"aulaglobal":{"command":"/usr/bin/node","args":["x.js"]}}}`
	for _, tc := range []struct {
		name  string
		opts  uninstallOpts
		input string
		gone  bool
	}{
		{"--yes never removes it", uninstallOpts{all: true, yes: true}, "", false},
		{"answer no", uninstallOpts{clients: "claude-desktop", clientsSet: true}, "n\ns\n", false},
		{"Enter means no", uninstallOpts{clients: "claude-desktop", clientsSet: true}, "\ns\n", false},
		{"answer yes", uninstallOpts{clients: "claude-desktop", clientsSet: true}, "s\ns\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			install := newFakeInstall(t)
			install.desktop(foreign)
			out, err := install.run(tc.opts, tc.input)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if got := !strings.Contains(testutil.ReadFile(t, install.desktopPath), "aulaglobal"); got != tc.gone {
				t.Fatalf("removed=%v\n%s", got, out)
			}
			if !tc.gone && !strings.Contains(out, "«/usr/bin/node»") {
				t.Fatalf("the foreign command is not shown\n%s", out)
			}
		})
	}
}

func TestUninstallClaudeLocalScopeIsExplainedNotTouched(t *testing.T) {
	install := newFakeInstall(t)
	install.claude.Scope = "local"
	out, err := install.run(uninstallOpts{clients: "claude-code", clientsSet: true, yes: true}, "")
	if err != nil || !strings.Contains(out, "claude mcp remove --scope local aulaglobal") || !strings.Contains(out, "No hay nada que hacer") {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, c := range install.claude.Calls {
		if c[1] == "remove" {
			t.Fatal("remove called")
		}
	}
}

func TestUninstallMalformedConfigGivesManualInstructions(t *testing.T) {
	install := newFakeInstall(t)
	install.desktop("{ not json")
	before := testutil.ReadFile(t, install.desktopPath)
	out, err := install.run(uninstallOpts{clients: "claude-desktop,codex", clientsSet: true, yes: true}, "")
	if err == nil {
		t.Fatalf("a failure must give a non-zero exit\n%s", out)
	}
	if !strings.Contains(out, "✗ Claude Desktop") || !strings.Contains(out, "Alternativa manual") || !strings.Contains(out, install.desktopPath) {
		t.Fatal(out)
	}
	if testutil.ReadFile(t, install.desktopPath) != before || exists(install.desktopPath+".bak") {
		t.Fatal("malformed file touched")
	}
	// The healthy assistant is still handled.
	if strings.Contains(testutil.ReadFile(t, install.codexPath), "aulaglobal") {
		t.Fatal("codex not processed")
	}
}

func TestUninstallRemoveFailureIsReportedWithManualSteps(t *testing.T) {
	install := newFakeInstall(t)
	install.claude.FailRemove = "private diagnostic"
	out, err := install.run(uninstallOpts{clients: "claude-code", clientsSet: true, yes: true}, "")
	if err == nil || !strings.Contains(out, "✗ Claude Code") || !strings.Contains(out, "claude mcp remove --scope user aulaglobal") || strings.Contains(out, "private diagnostic") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// ---- option 2 --------------------------------------------------------------------

func TestUninstallLogoutDeletesOnlyTheToken(t *testing.T) {
	install := newFakeInstall(t)
	out, err := install.run(uninstallOpts{logout: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if exists(install.configDir) {
		t.Fatal("empty config dir kept")
	}
	for _, path := range []string{install.exe, install.cacheDir, install.data, install.zshrc, install.codexPath} {
		if !exists(path) {
			t.Fatalf("%s removed by logout", path)
		}
	}
	if install.claude.Scope != "user" || !strings.Contains(out, "aulaglobal-mcp setup") || strings.Contains(strings.ToLower(out), "revoc") || strings.Contains(strings.ToLower(out), "servidor") {
		t.Fatalf("%s", out)
	}
	// Logging out twice is harmless.
	out, err = install.run(uninstallOpts{logout: true, yes: true}, "")
	if err != nil || !strings.Contains(out, "No hay ninguna clave") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUninstallLogoutCustomTokenLocations(t *testing.T) {
	install := newFakeInstall(t)
	custom := filepath.Join(install.root, "secrets", "token")
	testutil.WriteFile(t, custom, "tok")
	testutil.WriteFile(t, custom+".meta.json", "{}")
	testutil.WriteFile(t, filepath.Join(install.root, "secrets", "keep.txt"), "mine")
	install.e.tokenFile = custom
	// A custom path recorded in an assistant entry is found too.
	viaEntry := filepath.Join(install.root, "entry-secrets", "token")
	testutil.WriteFile(t, viaEntry, "tok")
	install.claude.Env = map[string]string{"AULAGLOBAL_TOKEN_FILE": viaEntry}
	out, err := install.run(uninstallOpts{logout: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if exists(custom) || exists(custom+".meta.json") || exists(viaEntry) || exists(filepath.Dir(viaEntry)) {
		t.Fatalf("token left behind\n%s", out)
	}
	if testutil.ReadFile(t, filepath.Join(install.root, "secrets", "keep.txt")) != "mine" || !isDir(filepath.Join(install.root, "secrets")) {
		t.Fatal("a non-empty folder or a foreign file was removed")
	}
	// The default token (not in use here) is untouched.
	if !exists(install.token) {
		t.Fatal("default token removed although another path was configured")
	}
}

func TestUninstallLogoutNeverDeletesLargeOrIrregularEntryTokens(t *testing.T) {
	install := newFakeInstall(t)
	big := filepath.Join(install.root, "thesis.docx")
	testutil.WriteFile(t, big, strings.Repeat("x", 100<<10))
	install.claude.Env = map[string]string{"AULAGLOBAL_TOKEN_FILE": big}
	if _, err := install.run(uninstallOpts{logout: true, yes: true}, ""); err != nil {
		t.Fatal(err)
	}
	if !exists(big) {
		t.Fatal("a file that cannot be a token was deleted")
	}
}

// ---- option 3 --------------------------------------------------------------------

func TestUninstallAllEndToEnd(t *testing.T) {
	install := newFakeInstall(t)
	out, err := install.run(uninstallOpts{all: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, path := range []string{install.exe, install.installDir, install.configDir, install.cacheDir, install.token} {
		if exists(path) {
			t.Errorf("%s still exists", path)
		}
	}
	if install.claude.Scope != "" || strings.Contains(testutil.ReadFile(t, install.codexPath), "aulaglobal") || !strings.Contains(testutil.ReadFile(t, install.codexPath), "[mcp_servers.other]") {
		t.Error("assistants not disconnected correctly")
	}
	for _, path := range []string{install.zshrc, install.bashrc} {
		if got := testutil.ReadFile(t, path); strings.Contains(got, pathMarker) || !strings.HasPrefix(got, "alias ll") && !strings.HasPrefix(got, "# bashrc") {
			t.Errorf("%s: %q", path, got)
		}
		if !strings.Contains(testutil.ReadFile(t, path+".bak"), pathMarker) {
			t.Errorf("%s.bak is not the original", path)
		}
	}
	// Materials and everything else stay: --yes does not imply --delete-data.
	if !exists(filepath.Join(install.data, "Fisica", "tema1.pdf")) || !exists(filepath.Join(install.home, "canary.txt")) || !isDir(filepath.Join(install.root, "Programs")) {
		t.Fatal("something outside the program was removed")
	}
	if !strings.Contains(out, install.data) || !strings.Contains(out, "se ha desinstalado") {
		t.Fatalf("%s", out)
	}
	// Idempotent.
	install.e.exe = filepath.Join(install.root, "elsewhere", "aulaglobal-mcp")
	testutil.WriteFile(t, install.e.exe, "x")
	if out, err = install.run(uninstallOpts{all: true, yes: true}, ""); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUninstallAllDeleteDataRemovesOnlyTheMarkedFolder(t *testing.T) {
	install := newFakeInstall(t)
	out, err := install.run(uninstallOpts{all: true, yes: true, deleteData: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if exists(install.data) {
		t.Fatal("materials kept despite --delete-data")
	}
	if !exists(filepath.Join(install.home, "canary.txt")) || !isDir(install.home) {
		t.Fatal("deleted outside the materials folder")
	}
}

func TestUninstallAllAsksAboutMaterialsDefaultingToKeep(t *testing.T) {
	for _, tc := range []struct {
		name   string // ASCII: it becomes part of a temp directory name
		answer string
		gone   bool
	}{{"enter", "\n", false}, {"n", "n\n", false}, {"s", "s\n", true}, {"si-accent", "sí\n", true}, {"other", "quizá\n", false}} {
		t.Run(tc.name, func(t *testing.T) {
			install := newFakeInstall(t)
			install.claude.Scope = ""
			out, err := install.run(uninstallOpts{all: true}, tc.answer+"s\n")
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if !strings.Contains(out, "¿Borrar también la carpeta AulaGlobal con tus materiales ("+install.data+")? [s/N]") {
				t.Fatalf("question\n%s", out)
			}
			if exists(install.data) == tc.gone {
				t.Fatalf("data folder gone=%v\n%s", !exists(install.data), out)
			}
		})
	}
	// Other options never ask about it.
	install := newFakeInstall(t)
	if out, _ := install.run(uninstallOpts{logout: true}, "s\n"); strings.Contains(out, "materiales") {
		t.Fatal("option 2 asked about materials")
	}
	install = newFakeInstall(t)
	if out, _ := install.run(uninstallOpts{clients: "codex", clientsSet: true}, "s\n"); strings.Contains(out, "materiales") {
		t.Fatal("option 1 asked about materials")
	}
}

func TestUninstallSuspiciousDataPathsAreRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		dir   func(install *fakeInstall) string
		setup func(install *fakeInstall, dir string)
	}{
		"home itself": {func(install *fakeInstall) string { return install.home }, nil},
		"no marker": {func(install *fakeInstall) string { return filepath.Join(install.root, "notes") }, func(install *fakeInstall, difference string) {
			testutil.WriteFile(install.t, filepath.Join(difference, "a.docx"), "x")
		}},
		"filesystem root": {func(install *fakeInstall) string {
			return filepath.VolumeName(install.root) + string(filepath.Separator)
		}, nil},
		"parent of home": {func(install *fakeInstall) string { return install.root }, func(install *fakeInstall, difference string) {}},
		"user folder": {func(install *fakeInstall) string { return filepath.Join(install.root, "Documents") }, func(install *fakeInstall, difference string) {
			testutil.WriteFile(install.t, filepath.Join(difference, dataMarker, "m"), "x")
		}},
		"program folder": {func(install *fakeInstall) string { return install.installDir }, func(install *fakeInstall, difference string) {}},
	} {
		t.Run(name, func(t *testing.T) {
			install := newFakeInstall(t)
			dir := tc.dir(install)
			if tc.setup != nil {
				tc.setup(install, dir)
			}
			install.claude.Scope = ""
			install.e.dataExtra = dir
			install.e.dataDefault = filepath.Join(install.root, "nothing-here") // so the default folder is not a candidate
			before := snapshot(t, install.root)
			out, err := install.run(uninstallOpts{all: true, yes: true, deleteData: true}, "")
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if !strings.Contains(out, "No se borrará") {
				t.Fatalf("refusal not explained\n%s", out)
			}
			after := snapshot(t, install.root)
			for rel := range before {
				if _, ok := after[rel]; !ok && !isProgramOrConfig(install, rel) {
					t.Fatalf("%s was deleted\n%s", rel, out)
				}
			}
		})
	}
}

func isProgramOrConfig(install *fakeInstall, rel string) bool {
	for _, path := range []string{install.exe, install.installDir, install.configDir, install.cacheDir} {
		r, _ := filepath.Rel(install.root, path)
		if rel == r || strings.HasPrefix(rel, r+string(os.PathSeparator)) {
			return true
		}
	}
	return rel == "home"+string(os.PathSeparator)+".zshrc.bak" || rel == "home"+string(os.PathSeparator)+".bashrc.bak" || strings.HasPrefix(rel, filepath.Join("home", ".codex"))
}

func TestUninstallCustomDataFolderOnlyDeletedByAnInteractiveAnswer(t *testing.T) {
	setup := func() *fakeInstall {
		install := newFakeInstall(t)
		install.claude.Scope = ""
		custom := filepath.Join(install.root, "Apuntes UC3M")
		testutil.WriteFile(t, filepath.Join(custom, dataMarker, "m"), "x")
		testutil.WriteFile(t, filepath.Join(custom, "a.pdf"), "x")
		install.e.dataExtra = custom
		install.e.dataDefault = filepath.Join(install.root, "nothing-here")
		return install
	}
	// --delete-data + --yes: a flag alone does not delete a folder that is not the default one.
	install := setup()
	out, err := install.run(uninstallOpts{all: true, yes: true, deleteData: true}, "")
	if err != nil || !exists(install.e.dataExtra) || !strings.Contains(out, "no es la carpeta predeterminada") {
		t.Fatalf("%v\n%s", err, out)
	}
	// Interactive: an extra warning, and only "s" deletes.
	install = setup()
	out, err = install.run(uninstallOpts{all: true}, "s\ns\n")
	if err != nil || exists(install.e.dataExtra) || !strings.Contains(out, "se borrará todo lo que haya dentro") {
		t.Fatalf("%v\n%s", err, out)
	}
	install = setup()
	if out, err = install.run(uninstallOpts{all: true}, "\ns\n"); err != nil || !exists(install.e.dataExtra) {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUninstallListsDifferentMaterialFolders(t *testing.T) {
	install := newFakeInstall(t)
	other := filepath.Join(install.root, "Apuntes")
	testutil.WriteFile(t, filepath.Join(other, dataMarker, "m"), "x")
	install.claude.Env = map[string]string{"AULAGLOBAL_DOWNLOAD_DIR": other}
	install.e.dataExtra = install.data
	out, err := install.run(uninstallOpts{all: true}, "n\nn\ns\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, difference := range []string{install.data, other} {
		if !strings.Contains(out, "materiales ("+difference+")") {
			t.Fatalf("no question for %s\n%s", difference, out)
		}
		if !exists(difference) {
			t.Fatalf("%s deleted without a yes", difference)
		}
	}
}

func TestUninstallAllInContainerKeepsTheBinary(t *testing.T) {
	install := newFakeInstall(t)
	install.e.inContainer = true
	out, err := install.run(uninstallOpts{all: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !exists(install.exe) || !strings.Contains(out, "contenedor") {
		t.Fatalf("%s", out)
	}
	if exists(install.token) || install.claude.Scope != "" {
		t.Fatal("the other steps should still run")
	}
	if !strings.Contains(testutil.ReadFile(t, install.zshrc), pathMarker) {
		t.Fatal("profiles are not this container's business")
	}
}

func TestUninstallProfileLineForAnotherFolderIsKept(t *testing.T) {
	install := newFakeInstall(t)
	other := `export PATH="/opt/other-copy:$PATH" ` + pathMarker
	testutil.WriteFile(t, install.zshrc, "x\n"+other+"\n")
	out, err := install.run(uninstallOpts{all: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := testutil.ReadFile(t, install.zshrc); got != "x\n"+other+"\n" || exists(install.zshrc+".bak") {
		t.Fatalf("%q", got)
	}
	if !strings.Contains(out, "es de otra carpeta") {
		t.Fatalf("%s", out)
	}
}

func TestUninstallDryRunChangesNothing(t *testing.T) {
	for _, opts := range []uninstallOpts{
		{all: true, dryRun: true, deleteData: true},
		{all: true, dryRun: true, yes: true, deleteData: true},
		{logout: true, dryRun: true},
		{clients: "claude-code,codex", clientsSet: true, dryRun: true},
	} {
		install := newFakeInstall(t)
		install.desktop(`{"mcpServers":{"aulaglobal":{"command":"/usr/bin/node"}}}`)
		before := snapshot(t, install.root)
		out, err := install.run(opts, "s\ns\ns\n")
		if err != nil {
			t.Fatalf("%+v: %v\n%s", opts, err, out)
		}
		if difference := sameSnapshot(before, snapshot(t, install.root)); difference != "" {
			t.Fatalf("%+v changed: %s", opts, difference)
		}
		for _, c := range install.claude.Calls {
			if c[1] == "remove" {
				t.Fatalf("%+v: claude remove called", opts)
			}
		}
		if !strings.Contains(out, "--dry-run") || !strings.Contains(out, "Plan:") {
			t.Fatalf("%s", out)
		}
	}
}

func TestUninstallConfirmationDefaultsToNo(t *testing.T) {
	for _, input := range []string{"\n", "n\n", "", "no\n"} {
		install := newFakeInstall(t)
		before := snapshot(t, install.root)
		out, err := install.run(uninstallOpts{all: true}, "n\n"+input) // data: no, continue: input
		if err != nil || !strings.Contains(out, "Cancelado") {
			t.Fatalf("%q: %v\n%s", input, err, out)
		}
		if difference := sameSnapshot(before, snapshot(t, install.root)); difference != "" {
			t.Fatalf("%q changed: %s", input, difference)
		}
	}
}

// ---- Windows semantics, with a fake registry and a fake scheduler ----------------

func TestUninstallAllWindowsSemantics(t *testing.T) {
	install := newFakeInstall(t)
	install.e.goos = "windows"
	install.e.exe = filepath.Join(install.installDir, "aulaglobal-mcp.exe")
	if err := os.Rename(install.exe, install.e.exe); err != nil {
		t.Fatal(err)
	}
	install.claude.Command = install.e.exe
	install.reg.value = `C:\Windows;` + install.installDir + `\;C:\Tools`
	var broadcast int
	install.e.winPath = install.reg
	install.e.afterPathEdit = func() { broadcast++ }
	install.e.removeBinary = func(exe string, rmDir bool) (stepStatus, string) {
		return removeBinaryWindows(exe, rmDir, func(c string) error { install.spawned = append(install.spawned, c); return nil })
	}
	out, err := install.run(uninstallOpts{all: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if install.reg.value != `C:\Windows;C:\Tools` || install.reg.sets != 1 || broadcast != 1 {
		t.Fatalf("PATH %q sets=%d broadcast=%d", install.reg.value, install.reg.sets, broadcast)
	}
	if exists(install.e.exe) || !exists(install.e.exe+".old") {
		t.Fatal("executable not parked as .old")
	}
	if len(install.spawned) != 1 || !strings.Contains(install.spawned[0], `del /f /q "`+install.e.exe+`.old"`) || !strings.Contains(install.spawned[0], `rmdir "`+install.installDir+`"`) {
		t.Fatalf("%v", install.spawned)
	}
	// Shell profiles are a Unix thing.
	if !strings.Contains(testutil.ReadFile(t, install.zshrc), pathMarker) {
		t.Fatal("profile edited on Windows")
	}
	if !strings.Contains(out, "se borrará en unos segundos") {
		t.Fatal(out)
	}
}

func TestUninstallWindowsPathFailuresAreReported(t *testing.T) {
	install := newFakeInstall(t)
	install.e.goos = "windows"
	install.e.winPath = &memPath{getErr: errors.New("denied")}
	out, err := install.run(uninstallOpts{all: true, yes: true}, "")
	if err != nil || !strings.Contains(out, "No se pudo leer el PATH") {
		t.Fatalf("%v\n%s", err, out)
	}
	install = newFakeInstall(t)
	install.e.goos = "windows"
	install.e.winPath = &memPath{value: install.installDir, setErr: errors.New("denied")}
	out, err = install.run(uninstallOpts{all: true, yes: true}, "")
	if err == nil || !strings.Contains(out, "✗ No se pudo cambiar el PATH") || !strings.Contains(out, install.installDir) {
		t.Fatalf("%v\n%s", err, out)
	}
	// An unrelated PATH is left alone and produces no step.
	install = newFakeInstall(t)
	reg := &memPath{value: `C:\Windows;C:\Tools`}
	install.e.goos, install.e.winPath = "windows", reg
	if out, err = install.run(uninstallOpts{all: true, yes: true}, ""); err != nil || reg.sets != 0 {
		t.Fatalf("%v sets=%d\n%s", err, reg.sets, out)
	}
}

// ---- a real subprocess standing in for the claude CLI ----------------------------

// TestMain lets the test binary double as a fake `claude`: tests copy it to a
// file named claude(.exe) and set AULAGLOBAL_FAKE_CLAUDE to its state file.
func TestMain(m *testing.M) {
	if state := os.Getenv("AULAGLOBAL_FAKE_CLAUDE"); state != "" {
		os.Exit(fakeClaudeMain(state, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeClaudeMain(stateFile string, args []string) int {
	content, _ := os.ReadFile(stateFile)
	command := strings.TrimSpace(string(content)) // "" = no server
	if len(args) < 3 || args[0] != "mcp" {
		return 2
	}
	switch args[1] {
	case "get":
		if command == "" {
			fmt.Println(`No MCP server found with name: "aulaglobal".`)
			return 1
		}
		fmt.Printf("aulaglobal:\n  Scope: User config (available in all your projects)\n  Type: stdio\n  Command: %s\n  Args: --transport stdio\n", command)
		return 0
	case "remove":
		if len(args) != 5 || args[2] != "--scope" || args[3] != "user" || args[4] != "aulaglobal" {
			fmt.Println("unexpected arguments")
			return 2
		}
		if command == "" {
			fmt.Println(`No MCP server found with name: "aulaglobal" in user config`)
			return 1
		}
		os.WriteFile(stateFile, nil, 0600)
		fmt.Println("Removed MCP server aulaglobal from user config")
		return 0
	}
	return 2
}

func TestClaudeCodeThroughARealSubprocess(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
	}
	cli := filepath.Join(dir, name)
	src, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	dst, err := os.OpenFile(cli, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(dst, src)
	src.Close()
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	exe := filepath.Join(dir, "aulaglobal-mcp")
	testutil.WriteFile(t, state, exe+"\n")
	t.Setenv("AULAGLOBAL_FAKE_CLAUDE", state)

	env := assistantstest.FileEnv(t)
	env.LookPath = func(n string) (string, error) {
		if n == "claude" {
			return cli, nil
		}
		return "", os.ErrNotExist
	}
	env.RunClaude = assistants.RealEnv(runtime.GOOS, "", "").RunClaude
	assistant := assistantstest.ByID(t, "claude-code")
	l := assistant.Find(env)
	if l.State != assistants.LinkPresent || !pointsToProgram(l.Command, exe, runtime.GOOS == "windows") {
		t.Fatalf("%+v", l)
	}
	if err := assistant.Remove(env); err != nil {
		t.Fatal(err)
	}
	if content, _ := os.ReadFile(state); strings.TrimSpace(string(content)) != "" {
		t.Fatal("the fake server was not removed")
	}
	if l := assistant.Find(env); l.State != assistants.LinkNone {
		t.Fatalf("%+v", l)
	}
	if err := assistant.Remove(env); err != nil {
		t.Fatal(err)
	}
}
