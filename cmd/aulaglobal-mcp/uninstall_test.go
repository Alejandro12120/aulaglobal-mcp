package main

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
type ufix struct {
	t                      *testing.T
	root, home, installDir string
	exe, token, configDir  string
	cacheDir, data         string
	e                      uninstallEnv
	claude                 *fakeClaudeCLI
	reg                    *memPath
	spawned                []string
	zshrc, bashrc          string
	codexPath, desktopPath string
	pathLine               string
}

func newUFix(t *testing.T) *ufix {
	t.Helper()
	root := t.TempDir()
	f := &ufix{t: t, root: root, home: filepath.Join(root, "home")}
	f.installDir = filepath.Join(root, "Programs", "aulaglobal-mcp")
	f.exe = filepath.Join(f.installDir, "aulaglobal-mcp")
	config := filepath.Join(f.home, "config")
	f.configDir = filepath.Join(config, "aulaglobal-mcp")
	f.cacheDir = filepath.Join(f.home, "cache", "aulaglobal-mcp")
	f.token = filepath.Join(f.configDir, "token")
	f.data = filepath.Join(f.home, "AulaGlobal")
	writeFile(t, f.exe, "binary")
	for _, n := range []string{"token", "token.private", "token.meta.json"} {
		writeFile(t, filepath.Join(f.configDir, n), "secret-"+n)
	}
	writeFile(t, filepath.Join(f.cacheDir, "update-check.json"), "{}")
	writeFile(t, filepath.Join(f.data, dataMarker, "manifest.json"), "{}")
	writeFile(t, filepath.Join(f.data, "Fisica", "tema1.pdf"), "pdf")
	f.pathLine = `export PATH="` + f.installDir + `:$PATH" ` + pathMarker
	f.zshrc = filepath.Join(f.home, ".zshrc")
	f.bashrc = filepath.Join(f.home, ".bashrc")
	writeFile(t, f.zshrc, "alias ll='ls -l'\n\n"+f.pathLine+"\n")
	writeFile(t, f.bashrc, "# bashrc\n\n"+f.pathLine+"\n")
	writeFile(t, filepath.Join(f.home, "canary.txt"), "mine")

	f.claude = &fakeClaudeCLI{scope: "user", command: f.exe}
	ce := clientEnv{GOOS: runtime.GOOS, Home: f.home, Config: config}
	ce.Look = func(n string) (string, error) {
		if n == "claude" {
			return "fake-claude", nil
		}
		return "", os.ErrNotExist
	}
	ce.Stat = func(p string) (os.FileInfo, error) {
		if !strings.HasPrefix(p, f.home+string(os.PathSeparator)) {
			return nil, os.ErrNotExist
		}
		return os.Stat(p)
	}
	ce.Run = f.claude.run
	f.codexPath = filepath.Join(f.home, ".codex", "config.toml")
	writeFile(t, f.codexPath, "model = \"x\"\n\n[mcp_servers.other]\ncommand = \"keep\"\n\n[mcp_servers.aulaglobal]\ncommand = '"+f.exe+"'\nargs = ['--transport', 'stdio']\n")
	f.reg = &memPath{}
	f.e = uninstallEnv{
		ce: ce, list: []assistant{claudeAssistant{}, codexAssistant(), desktopAssistant()}, goos: "linux", home: f.home,
		exe: f.exe, tokenFile: f.token, configDir: f.configDir, cacheDir: f.cacheDir, dataDefault: f.data,
		interactive: true, getenv: func(string) string { return "" }, removeBinary: removeBinaryUnix,
	}
	return f
}

func (f *ufix) desktop(content string) {
	f.desktopPath = userpaths.Claude(f.e.ce.GOOS, f.home, f.e.ce.Config)
	writeFile(f.t, f.desktopPath, content)
}

func (f *ufix) run(o uninstallOpts, input string) (string, error) {
	var out strings.Builder
	err := runUninstall(setupUI{in: strings.NewReader(input), out: &out}, f.e, o)
	return out.String(), err
}

// snapshot hashes every file and directory under root.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			m[rel] = "dir"
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		m[rel] = fmt.Sprintf("%x", sha256.Sum256(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func sameSnapshot(a, b map[string]string) string {
	var diff []string
	for k, v := range a {
		if b[k] != v {
			diff = append(diff, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			diff = append(diff, k)
		}
	}
	sort.Strings(diff)
	return strings.Join(diff, ", ")
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func (f *ufix) transcript(out string) string {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	out = strings.ReplaceAll(out, f.root, "<tmp>")
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
	o, _ := parseUninstallFlags([]string{"--yes", "--all"})
	if o.deleteData {
		t.Fatal("--yes implied --delete-data")
	}
}

func TestUninstallHelpExitsCleanly(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = old }()
	for _, a := range []string{"--help", "-h"} {
		if err := uninstall([]string{a}); err != nil {
			t.Fatal(err)
		}
	}
	os.Stdout = old
	b, _ := os.ReadFile(f.Name())
	for _, want := range []string{"Uso: aulaglobal-mcp uninstall", "--delete-data", "--dry-run", "claude-code,claude-desktop,codex,opencode,antigravity", "nunca implica --delete-data"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("help lacks %q", want)
		}
	}
	if err := uninstall([]string{"--delete-data"}); err == nil || !strings.Contains(err.Error(), "Uso:") {
		t.Fatalf("usage error lacks help: %v", err)
	}
}

func TestUninstallWithoutTerminalOrFlagsChangesNothing(t *testing.T) {
	f := newUFix(t)
	f.e.interactive = false
	before := snapshot(t, f.root)
	out, err := f.run(uninstallOpts{}, "")
	if err == nil || !strings.Contains(out, "Uso: aulaglobal-mcp uninstall") {
		t.Fatalf("%v\n%s", err, out)
	}
	if d := sameSnapshot(before, snapshot(t, f.root)); d != "" || f.claude.scope != "user" || len(f.claude.calls) != 0 && f.claude.calls[len(f.claude.calls)-1][1] == "remove" {
		t.Fatal("changed:", d)
	}
	// With flags but no way to confirm, it asks for --yes instead of guessing.
	if _, err = f.run(uninstallOpts{all: true}, ""); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("%v", err)
	}
	if d := sameSnapshot(before, snapshot(t, f.root)); d != "" {
		t.Fatal("changed:", d)
	}
	// --dry-run needs no confirmation.
	if _, err = f.run(uninstallOpts{all: true, dryRun: true}, ""); err != nil {
		t.Fatal(err)
	}
}

// ---- goldens -------------------------------------------------------------------

func TestUninstallTranscripts(t *testing.T) {
	for _, sc := range []string{"disconnect", "logout", "all", "dry-run", "nothing-to-do"} {
		t.Run(sc, func(t *testing.T) {
			f := newUFix(t)
			f.desktop(`{"mcpServers":{"aulaglobal":{"command":"/usr/bin/node","args":["server.js"]},"keep":{"command":"k"}}}`)
			o := uninstallOpts{}
			input := ""
			switch sc {
			case "disconnect":
				input = "\n\nn\ns\n" // menu 1, all, keep the foreign entry, continue
			case "logout":
				input = "2\ns\n"
			case "all":
				input = "3\n\nn\ns\n" // menu 3, foreign entry: Enter (no), data folder: n, continue
			case "dry-run":
				o.all, o.dryRun = true, true
				input = "n\ns\n" // foreign: no; data folder: yes (shown in the plan only)
			case "nothing-to-do":
				f.claude.scope = ""
				os.Remove(f.codexPath)
				os.Remove(f.desktopPath)
				input = "\n"
			}
			out, err := f.run(o, input)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			compareGolden(t, "uninstall-"+sc, f.transcript(out))
		})
	}
}

func TestUninstallMenuInvalidInputCancels(t *testing.T) {
	f := newUFix(t)
	before := snapshot(t, f.root)
	out, err := f.run(uninstallOpts{}, "x\ny\nz\n")
	if err != nil || !strings.Contains(out, "Escribe 1, 2 o 3") || !strings.Contains(out, "No se ha cambiado nada") {
		t.Fatalf("%v\n%s", err, out)
	}
	if d := sameSnapshot(before, snapshot(t, f.root)); d != "" {
		t.Fatal(d)
	}
}

// ---- option 1 --------------------------------------------------------------------

func TestUninstallDisconnectSelectedClients(t *testing.T) {
	f := newUFix(t)
	out, err := f.run(uninstallOpts{clients: "codex", clientsSet: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(readFile(t, f.codexPath), "aulaglobal") || !strings.Contains(readFile(t, f.codexPath), "[mcp_servers.other]") {
		t.Fatal(readFile(t, f.codexPath))
	}
	if f.claude.scope != "user" {
		t.Fatal("Claude Code was not selected but was changed")
	}
	// Naming an assistant that has nothing is reported, not an error.
	out, err = f.run(uninstallOpts{clients: "codex,opencode", clientsSet: true, yes: true}, "")
	if err != nil || !strings.Contains(out, "Codex / ChatGPT: no tiene AulaGlobal conectado") {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, p := range []string{f.exe, f.token, f.data, f.zshrc} {
		if !exists(p) {
			t.Fatalf("%s removed by option 1", p)
		}
	}
}

func TestUninstallInteractiveSelectionByNumber(t *testing.T) {
	f := newUFix(t)
	// Claude Code is 1 and Codex is 2: choose only Codex.
	out, err := f.run(uninstallOpts{}, "1\n2\ns\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if f.claude.scope != "user" || strings.Contains(readFile(t, f.codexPath), "aulaglobal") {
		t.Fatalf("selection ignored\n%s", out)
	}
	// 0 means none; invalid answers are asked again.
	f = newUFix(t)
	before := snapshot(t, f.root)
	out, err = f.run(uninstallOpts{}, "1\n9\n0\n")
	if err != nil || !strings.Contains(out, "No te he entendido") {
		t.Fatalf("%v\n%s", err, out)
	}
	if d := sameSnapshot(before, snapshot(t, f.root)); d != "" {
		t.Fatal(d)
	}
}

func TestUninstallForeignEntriesNeedAnExplicitYes(t *testing.T) {
	foreign := `{"mcpServers":{"aulaglobal":{"command":"/usr/bin/node","args":["x.js"]}}}`
	for _, tc := range []struct {
		name  string
		o     uninstallOpts
		input string
		gone  bool
	}{
		{"--yes never removes it", uninstallOpts{all: true, yes: true}, "", false},
		{"answer no", uninstallOpts{clients: "claude-desktop", clientsSet: true}, "n\ns\n", false},
		{"Enter means no", uninstallOpts{clients: "claude-desktop", clientsSet: true}, "\ns\n", false},
		{"answer yes", uninstallOpts{clients: "claude-desktop", clientsSet: true}, "s\ns\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newUFix(t)
			f.desktop(foreign)
			out, err := f.run(tc.o, tc.input)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if got := !strings.Contains(readFile(t, f.desktopPath), "aulaglobal"); got != tc.gone {
				t.Fatalf("removed=%v\n%s", got, out)
			}
			if !tc.gone && !strings.Contains(out, "«/usr/bin/node»") {
				t.Fatalf("the foreign command is not shown\n%s", out)
			}
		})
	}
}

func TestUninstallClaudeLocalScopeIsExplainedNotTouched(t *testing.T) {
	f := newUFix(t)
	f.claude.scope = "local"
	out, err := f.run(uninstallOpts{clients: "claude-code", clientsSet: true, yes: true}, "")
	if err != nil || !strings.Contains(out, "claude mcp remove --scope local aulaglobal") || !strings.Contains(out, "No hay nada que hacer") {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, c := range f.claude.calls {
		if c[1] == "remove" {
			t.Fatal("remove called")
		}
	}
}

func TestUninstallMalformedConfigGivesManualInstructions(t *testing.T) {
	f := newUFix(t)
	f.desktop("{ not json")
	before := readFile(t, f.desktopPath)
	out, err := f.run(uninstallOpts{clients: "claude-desktop,codex", clientsSet: true, yes: true}, "")
	if err == nil {
		t.Fatalf("a failure must give a non-zero exit\n%s", out)
	}
	if !strings.Contains(out, "✗ Claude Desktop") || !strings.Contains(out, "Alternativa manual") || !strings.Contains(out, f.desktopPath) {
		t.Fatal(out)
	}
	if readFile(t, f.desktopPath) != before || exists(f.desktopPath+".bak") {
		t.Fatal("malformed file touched")
	}
	// The healthy assistant is still handled.
	if strings.Contains(readFile(t, f.codexPath), "aulaglobal") {
		t.Fatal("codex not processed")
	}
}

func TestUninstallRemoveFailureIsReportedWithManualSteps(t *testing.T) {
	f := newUFix(t)
	f.claude.failRemove = "private diagnostic"
	out, err := f.run(uninstallOpts{clients: "claude-code", clientsSet: true, yes: true}, "")
	if err == nil || !strings.Contains(out, "✗ Claude Code") || !strings.Contains(out, "claude mcp remove --scope user aulaglobal") || strings.Contains(out, "private diagnostic") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// ---- option 2 --------------------------------------------------------------------

func TestUninstallLogoutDeletesOnlyTheToken(t *testing.T) {
	f := newUFix(t)
	out, err := f.run(uninstallOpts{logout: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if exists(f.configDir) {
		t.Fatal("empty config dir kept")
	}
	for _, p := range []string{f.exe, f.cacheDir, f.data, f.zshrc, f.codexPath} {
		if !exists(p) {
			t.Fatalf("%s removed by logout", p)
		}
	}
	if f.claude.scope != "user" || !strings.Contains(out, "aulaglobal-mcp setup") || strings.Contains(strings.ToLower(out), "revoc") || strings.Contains(strings.ToLower(out), "servidor") {
		t.Fatalf("%s", out)
	}
	// Logging out twice is harmless.
	out, err = f.run(uninstallOpts{logout: true, yes: true}, "")
	if err != nil || !strings.Contains(out, "No hay ninguna clave") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUninstallLogoutCustomTokenLocations(t *testing.T) {
	f := newUFix(t)
	custom := filepath.Join(f.root, "secrets", "token")
	writeFile(t, custom, "tok")
	writeFile(t, custom+".meta.json", "{}")
	writeFile(t, filepath.Join(f.root, "secrets", "keep.txt"), "mine")
	f.e.tokenFile = custom
	// A custom path recorded in an assistant entry is found too.
	viaEntry := filepath.Join(f.root, "entry-secrets", "token")
	writeFile(t, viaEntry, "tok")
	f.claude.env = map[string]string{"AULAGLOBAL_TOKEN_FILE": viaEntry}
	out, err := f.run(uninstallOpts{logout: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if exists(custom) || exists(custom+".meta.json") || exists(viaEntry) || exists(filepath.Dir(viaEntry)) {
		t.Fatalf("token left behind\n%s", out)
	}
	if readFile(t, filepath.Join(f.root, "secrets", "keep.txt")) != "mine" || !isDir(filepath.Join(f.root, "secrets")) {
		t.Fatal("a non-empty folder or a foreign file was removed")
	}
	// The default token (not in use here) is untouched.
	if !exists(f.token) {
		t.Fatal("default token removed although another path was configured")
	}
}

func TestUninstallLogoutNeverDeletesLargeOrIrregularEntryTokens(t *testing.T) {
	f := newUFix(t)
	big := filepath.Join(f.root, "thesis.docx")
	writeFile(t, big, strings.Repeat("x", 100<<10))
	f.claude.env = map[string]string{"AULAGLOBAL_TOKEN_FILE": big}
	if _, err := f.run(uninstallOpts{logout: true, yes: true}, ""); err != nil {
		t.Fatal(err)
	}
	if !exists(big) {
		t.Fatal("a file that cannot be a token was deleted")
	}
}

// ---- option 3 --------------------------------------------------------------------

func TestUninstallAllEndToEnd(t *testing.T) {
	f := newUFix(t)
	out, err := f.run(uninstallOpts{all: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, p := range []string{f.exe, f.installDir, f.configDir, f.cacheDir, f.token} {
		if exists(p) {
			t.Errorf("%s still exists", p)
		}
	}
	if f.claude.scope != "" || strings.Contains(readFile(t, f.codexPath), "aulaglobal") || !strings.Contains(readFile(t, f.codexPath), "[mcp_servers.other]") {
		t.Error("assistants not disconnected correctly")
	}
	for _, p := range []string{f.zshrc, f.bashrc} {
		if got := readFile(t, p); strings.Contains(got, pathMarker) || !strings.HasPrefix(got, "alias ll") && !strings.HasPrefix(got, "# bashrc") {
			t.Errorf("%s: %q", p, got)
		}
		if !strings.Contains(readFile(t, p+".bak"), pathMarker) {
			t.Errorf("%s.bak is not the original", p)
		}
	}
	// Materials and everything else stay: --yes does not imply --delete-data.
	if !exists(filepath.Join(f.data, "Fisica", "tema1.pdf")) || !exists(filepath.Join(f.home, "canary.txt")) || !isDir(filepath.Join(f.root, "Programs")) {
		t.Fatal("something outside the program was removed")
	}
	if !strings.Contains(out, f.data) || !strings.Contains(out, "se ha desinstalado") {
		t.Fatalf("%s", out)
	}
	// Idempotent.
	f.e.exe = filepath.Join(f.root, "elsewhere", "aulaglobal-mcp")
	writeFile(t, f.e.exe, "x")
	if out, err = f.run(uninstallOpts{all: true, yes: true}, ""); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUninstallAllDeleteDataRemovesOnlyTheMarkedFolder(t *testing.T) {
	f := newUFix(t)
	out, err := f.run(uninstallOpts{all: true, yes: true, deleteData: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if exists(f.data) {
		t.Fatal("materials kept despite --delete-data")
	}
	if !exists(filepath.Join(f.home, "canary.txt")) || !isDir(f.home) {
		t.Fatal("deleted outside the materials folder")
	}
}

func TestUninstallAllAsksAboutMaterialsDefaultingToKeep(t *testing.T) {
	for _, tc := range []struct {
		answer string
		gone   bool
	}{{"\n", false}, {"n\n", false}, {"s\n", true}, {"sí\n", true}, {"quizá\n", false}} {
		t.Run(strings.TrimSpace(tc.answer), func(t *testing.T) {
			f := newUFix(t)
			f.claude.scope = ""
			out, err := f.run(uninstallOpts{all: true}, tc.answer+"s\n")
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if !strings.Contains(out, "¿Borrar también la carpeta AulaGlobal con tus materiales ("+f.data+")? [s/N]") {
				t.Fatalf("question\n%s", out)
			}
			if exists(f.data) == tc.gone {
				t.Fatalf("data folder gone=%v\n%s", !exists(f.data), out)
			}
		})
	}
	// Other options never ask about it.
	f := newUFix(t)
	if out, _ := f.run(uninstallOpts{logout: true}, "s\n"); strings.Contains(out, "materiales") {
		t.Fatal("option 2 asked about materials")
	}
	f = newUFix(t)
	if out, _ := f.run(uninstallOpts{clients: "codex", clientsSet: true}, "s\n"); strings.Contains(out, "materiales") {
		t.Fatal("option 1 asked about materials")
	}
}

func TestUninstallSuspiciousDataPathsAreRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		dir   func(f *ufix) string
		setup func(f *ufix, dir string)
	}{
		"home itself":     {func(f *ufix) string { return f.home }, nil},
		"no marker":       {func(f *ufix) string { return filepath.Join(f.root, "notes") }, func(f *ufix, d string) { writeFile(f.t, filepath.Join(d, "a.docx"), "x") }},
		"filesystem root": {func(f *ufix) string { return filepath.VolumeName(f.root) + string(filepath.Separator) }, nil},
		"parent of home":  {func(f *ufix) string { return f.root }, func(f *ufix, d string) {}},
		"user folder":     {func(f *ufix) string { return filepath.Join(f.root, "Documents") }, func(f *ufix, d string) { writeFile(f.t, filepath.Join(d, dataMarker, "m"), "x") }},
		"program folder":  {func(f *ufix) string { return f.installDir }, func(f *ufix, d string) {}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newUFix(t)
			dir := tc.dir(f)
			if tc.setup != nil {
				tc.setup(f, dir)
			}
			f.claude.scope = ""
			f.e.dataExtra = dir
			f.e.dataDefault = filepath.Join(f.root, "nothing-here") // so the default folder is not a candidate
			before := snapshot(t, f.root)
			out, err := f.run(uninstallOpts{all: true, yes: true, deleteData: true}, "")
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if !strings.Contains(out, "No se borrará") {
				t.Fatalf("refusal not explained\n%s", out)
			}
			after := snapshot(t, f.root)
			for rel := range before {
				if _, ok := after[rel]; !ok && !isProgramOrConfig(f, rel) {
					t.Fatalf("%s was deleted\n%s", rel, out)
				}
			}
		})
	}
}

func isProgramOrConfig(f *ufix, rel string) bool {
	for _, p := range []string{f.exe, f.installDir, f.configDir, f.cacheDir} {
		r, _ := filepath.Rel(f.root, p)
		if rel == r || strings.HasPrefix(rel, r+string(os.PathSeparator)) {
			return true
		}
	}
	return rel == "home"+string(os.PathSeparator)+".zshrc.bak" || rel == "home"+string(os.PathSeparator)+".bashrc.bak" || strings.HasPrefix(rel, filepath.Join("home", ".codex"))
}

func TestUninstallCustomDataFolderOnlyDeletedByAnInteractiveAnswer(t *testing.T) {
	setup := func() *ufix {
		f := newUFix(t)
		f.claude.scope = ""
		custom := filepath.Join(f.root, "Apuntes UC3M")
		writeFile(t, filepath.Join(custom, dataMarker, "m"), "x")
		writeFile(t, filepath.Join(custom, "a.pdf"), "x")
		f.e.dataExtra = custom
		f.e.dataDefault = filepath.Join(f.root, "nothing-here")
		return f
	}
	// --delete-data + --yes: a flag alone does not delete a folder that is not the default one.
	f := setup()
	out, err := f.run(uninstallOpts{all: true, yes: true, deleteData: true}, "")
	if err != nil || !exists(f.e.dataExtra) || !strings.Contains(out, "no es la carpeta predeterminada") {
		t.Fatalf("%v\n%s", err, out)
	}
	// Interactive: an extra warning, and only "s" deletes.
	f = setup()
	out, err = f.run(uninstallOpts{all: true}, "s\ns\n")
	if err != nil || exists(f.e.dataExtra) || !strings.Contains(out, "se borrará todo lo que haya dentro") {
		t.Fatalf("%v\n%s", err, out)
	}
	f = setup()
	if out, err = f.run(uninstallOpts{all: true}, "\ns\n"); err != nil || !exists(f.e.dataExtra) {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUninstallListsDifferentMaterialFolders(t *testing.T) {
	f := newUFix(t)
	other := filepath.Join(f.root, "Apuntes")
	writeFile(t, filepath.Join(other, dataMarker, "m"), "x")
	f.claude.env = map[string]string{"AULAGLOBAL_DOWNLOAD_DIR": other}
	f.e.dataExtra = f.data
	out, err := f.run(uninstallOpts{all: true}, "n\nn\ns\n")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, d := range []string{f.data, other} {
		if !strings.Contains(out, "materiales ("+d+")") {
			t.Fatalf("no question for %s\n%s", d, out)
		}
		if !exists(d) {
			t.Fatalf("%s deleted without a yes", d)
		}
	}
}

func TestUninstallAllInContainerKeepsTheBinary(t *testing.T) {
	f := newUFix(t)
	f.e.inContainer = true
	out, err := f.run(uninstallOpts{all: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !exists(f.exe) || !strings.Contains(out, "contenedor") {
		t.Fatalf("%s", out)
	}
	if exists(f.token) || f.claude.scope != "" {
		t.Fatal("the other steps should still run")
	}
	if !strings.Contains(readFile(t, f.zshrc), pathMarker) {
		t.Fatal("profiles are not this container's business")
	}
}

func TestUninstallProfileLineForAnotherFolderIsKept(t *testing.T) {
	f := newUFix(t)
	other := `export PATH="/opt/other-copy:$PATH" ` + pathMarker
	writeFile(t, f.zshrc, "x\n"+other+"\n")
	out, err := f.run(uninstallOpts{all: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := readFile(t, f.zshrc); got != "x\n"+other+"\n" || exists(f.zshrc+".bak") {
		t.Fatalf("%q", got)
	}
	if !strings.Contains(out, "es de otra carpeta") {
		t.Fatalf("%s", out)
	}
}

func TestUninstallDryRunChangesNothing(t *testing.T) {
	for _, o := range []uninstallOpts{
		{all: true, dryRun: true, deleteData: true},
		{all: true, dryRun: true, yes: true, deleteData: true},
		{logout: true, dryRun: true},
		{clients: "claude-code,codex", clientsSet: true, dryRun: true},
	} {
		f := newUFix(t)
		f.desktop(`{"mcpServers":{"aulaglobal":{"command":"/usr/bin/node"}}}`)
		before := snapshot(t, f.root)
		out, err := f.run(o, "s\ns\ns\n")
		if err != nil {
			t.Fatalf("%+v: %v\n%s", o, err, out)
		}
		if d := sameSnapshot(before, snapshot(t, f.root)); d != "" {
			t.Fatalf("%+v changed: %s", o, d)
		}
		for _, c := range f.claude.calls {
			if c[1] == "remove" {
				t.Fatalf("%+v: claude remove called", o)
			}
		}
		if !strings.Contains(out, "--dry-run") || !strings.Contains(out, "Plan:") {
			t.Fatalf("%s", out)
		}
	}
}

func TestUninstallConfirmationDefaultsToNo(t *testing.T) {
	for _, input := range []string{"\n", "n\n", "", "no\n"} {
		f := newUFix(t)
		before := snapshot(t, f.root)
		out, err := f.run(uninstallOpts{all: true}, "n\n"+input) // data: no, continue: input
		if err != nil || !strings.Contains(out, "Cancelado") {
			t.Fatalf("%q: %v\n%s", input, err, out)
		}
		if d := sameSnapshot(before, snapshot(t, f.root)); d != "" {
			t.Fatalf("%q changed: %s", input, d)
		}
	}
}

// ---- Windows semantics, with a fake registry and a fake scheduler ----------------

func TestUninstallAllWindowsSemantics(t *testing.T) {
	f := newUFix(t)
	f.e.goos = "windows"
	f.e.exe = filepath.Join(f.installDir, "aulaglobal-mcp.exe")
	if err := os.Rename(f.exe, f.e.exe); err != nil {
		t.Fatal(err)
	}
	f.claude.command = f.e.exe
	f.reg.value = `C:\Windows;` + f.installDir + `\;C:\Tools`
	var broadcast int
	f.e.winPath = f.reg
	f.e.afterPathEdit = func() { broadcast++ }
	f.e.removeBinary = func(exe string, rmDir bool) (stepStatus, string) {
		return removeBinaryWindows(exe, rmDir, func(c string) error { f.spawned = append(f.spawned, c); return nil })
	}
	out, err := f.run(uninstallOpts{all: true, yes: true}, "")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if f.reg.value != `C:\Windows;C:\Tools` || f.reg.sets != 1 || broadcast != 1 {
		t.Fatalf("PATH %q sets=%d broadcast=%d", f.reg.value, f.reg.sets, broadcast)
	}
	if exists(f.e.exe) || !exists(f.e.exe+".old") {
		t.Fatal("executable not parked as .old")
	}
	if len(f.spawned) != 1 || !strings.Contains(f.spawned[0], `del /f /q "`+f.e.exe+`.old"`) || !strings.Contains(f.spawned[0], `rmdir "`+f.installDir+`"`) {
		t.Fatalf("%v", f.spawned)
	}
	// Shell profiles are a Unix thing.
	if !strings.Contains(readFile(t, f.zshrc), pathMarker) {
		t.Fatal("profile edited on Windows")
	}
	if !strings.Contains(out, "se borrará en unos segundos") {
		t.Fatal(out)
	}
}

func TestUninstallWindowsPathFailuresAreReported(t *testing.T) {
	f := newUFix(t)
	f.e.goos = "windows"
	f.e.winPath = &memPath{getErr: errors.New("denied")}
	out, err := f.run(uninstallOpts{all: true, yes: true}, "")
	if err != nil || !strings.Contains(out, "No se pudo leer el PATH") {
		t.Fatalf("%v\n%s", err, out)
	}
	f = newUFix(t)
	f.e.goos = "windows"
	f.e.winPath = &memPath{value: f.installDir, setErr: errors.New("denied")}
	out, err = f.run(uninstallOpts{all: true, yes: true}, "")
	if err == nil || !strings.Contains(out, "✗ No se pudo cambiar el PATH") || !strings.Contains(out, f.installDir) {
		t.Fatalf("%v\n%s", err, out)
	}
	// An unrelated PATH is left alone and produces no step.
	f = newUFix(t)
	reg := &memPath{value: `C:\Windows;C:\Tools`}
	f.e.goos, f.e.winPath = "windows", reg
	if out, err = f.run(uninstallOpts{all: true, yes: true}, ""); err != nil || reg.sets != 0 {
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
	b, _ := os.ReadFile(stateFile)
	command := strings.TrimSpace(string(b)) // "" = no server
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
	writeFile(t, state, exe+"\n")
	t.Setenv("AULAGLOBAL_FAKE_CLAUDE", state)

	e := fileClientEnv(t)
	e.Look = func(n string) (string, error) {
		if n == "claude" {
			return cli, nil
		}
		return "", os.ErrNotExist
	}
	e.Run = runClaude
	a := claudeAssistant{}
	l := a.Find(e)
	if l.state != linkPresent || !pointsToProgram(l.command, exe, runtime.GOOS == "windows") {
		t.Fatalf("%+v", l)
	}
	if err := a.Remove(e); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(state); strings.TrimSpace(string(b)) != "" {
		t.Fatal("the fake server was not removed")
	}
	if l := a.Find(e); l.state != linkNone {
		t.Fatalf("%+v", l)
	}
	if err := a.Remove(e); err != nil {
		t.Fatal(err)
	}
}
