package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"aulaglobal-mcp/internal/selfupdate"
	"aulaglobal-mcp/internal/userpaths"

	"golang.org/x/term"
)

const uninstallUsage = `Uso: aulaglobal-mcp uninstall [opciones]

Sin opciones abre un menú con tres acciones:
  1. Desconectar AulaGlobal de algunos asistentes
  2. Cerrar la sesión de AulaGlobal (borrar la clave de acceso)
  3. Desinstalar todo (asistentes, clave, caché, PATH y el propio programa)

Antes de cambiar nada se muestra el plan y se pide confirmación.

Opciones (para usarlo sin menú):
  --clients ids   desconecta esos asistentes (separados por comas): ` + "%s" + `
  --logout        borra la clave de acceso
  --all           desinstala todo; conserva tu carpeta AulaGlobal
  --delete-data   con --all, borra también la carpeta de apuntes (solo si es
                  la carpeta predeterminada y tiene la marca de este programa)
  --yes           no pide confirmación (nunca implica --delete-data ni quita
                  entradas que apunten a otro programa)
  --dry-run       muestra el plan sin cambiar nada
  --secrets-dir   carpeta de la clave de acceso, como en setup
  --data-dir      carpeta de apuntes, como en setup

La carpeta de apuntes se pregunta aparte, solo al desinstalar todo, y por
defecto se conserva.
`

func uninstallHelp() string { return fmt.Sprintf(uninstallUsage, clientIDs()) }

type uninstallOpts struct {
	clients                 string
	clientsSet              bool
	logout, all             bool
	deleteData, yes, dryRun bool
	secretsDir, dataDir     string
}

func parseUninstallFlags(args []string) (uninstallOpts, error) {
	var o uninstallOpts
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&o.clients, "clients", "", "")
	flags.BoolVar(&o.logout, "logout", false, "")
	flags.BoolVar(&o.all, "all", false, "")
	flags.BoolVar(&o.deleteData, "delete-data", false, "")
	flags.BoolVar(&o.yes, "yes", false, "")
	flags.BoolVar(&o.dryRun, "dry-run", false, "")
	flags.StringVar(&o.secretsDir, "secrets-dir", "", "")
	flags.StringVar(&o.dataDir, "data-dir", "", "")
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	flags.Visit(func(f *flag.Flag) { o.clientsSet = o.clientsSet || f.Name == "clients" })
	if flags.NArg() > 0 {
		return o, fmt.Errorf("argumento inesperado %q", flags.Arg(0))
	}
	n := 0
	for _, b := range []bool{o.clientsSet, o.logout, o.all} {
		if b {
			n++
		}
	}
	switch {
	case n > 1:
		return o, errors.New("usa solo una de las opciones --clients, --logout y --all")
	case o.deleteData && !o.all:
		return o, errors.New("--delete-data solo se puede usar junto con --all")
	case o.clientsSet && strings.TrimSpace(o.clients) == "":
		return o, errors.New("--clients necesita al menos un asistente: " + clientIDs())
	}
	if o.clientsSet {
		if _, err := selectAssistants(o.clients); err != nil {
			return o, err
		}
	}
	return o, nil
}

// uninstallEnv is everything uninstall touches, injected so tests run in a
// temporary directory with fake paths, a fake registry and a fake executable.
type uninstallEnv struct {
	ce            clientEnv
	list          []assistant
	goos          string
	home          string
	exe           string // resolved path of the running program
	tokenFile     string
	configDir     string // <UserConfigDir>/aulaglobal-mcp
	cacheDir      string // <UserCacheDir>/aulaglobal-mcp
	dataDefault   string
	dataExtra     string // --data-dir or AULAGLOBAL_DOWNLOAD_DIR, if any
	zdotdir, xdg  string
	inContainer   bool
	interactive   bool
	winPath       pathStore
	getenv        func(string) string
	removeBinary  func(exe string, rmDir bool) (stepStatus, string)
	afterPathEdit func()
}

// uninstall is the `aulaglobal-mcp uninstall` entry point.
func uninstall(args []string) error {
	o, err := parseUninstallFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Print(uninstallHelp())
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w\n\n%s", err, uninstallHelp())
	}
	token, data, err := userpaths.Defaults()
	if err != nil {
		return err
	}
	token = env("AULAGLOBAL_TOKEN_FILE", token)
	if o.secretsDir != "" {
		token = filepath.Join(o.secretsDir, "token")
	}
	if token, err = filepath.Abs(token); err != nil {
		return err
	}
	extra := env("AULAGLOBAL_DOWNLOAD_DIR", "")
	if o.dataDir != "" {
		extra = o.dataDir
	}
	if extra != "" {
		if extra, err = filepath.Abs(extra); err != nil {
			return err
		}
	}
	exe, err := selfupdate.ResolveExecutable()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	cache, err := userpaths.UpdateCache()
	if err != nil {
		return err
	}
	e := uninstallEnv{
		ce: realClientEnv(runtime.GOOS, home, config), list: assistants, goos: runtime.GOOS, home: home, exe: exe,
		tokenFile: token, configDir: filepath.Join(config, programName), cacheDir: filepath.Dir(cache),
		dataDefault: data, dataExtra: extra, zdotdir: os.Getenv("ZDOTDIR"), xdg: os.Getenv("XDG_CONFIG_HOME"),
		inContainer: selfupdate.InContainer(), interactive: term.IsTerminal(int(os.Stdin.Fd())),
		winPath: realPathStore(), getenv: os.Getenv, afterPathEdit: broadcastEnvironmentChange,
	}
	if runtime.GOOS == "windows" {
		e.removeBinary = func(exe string, rmDir bool) (stepStatus, string) {
			return removeBinaryWindows(exe, rmDir, spawnDetached)
		}
	} else {
		e.removeBinary = removeBinaryUnix
	}
	return runUninstall(setupUI{in: os.Stdin, out: os.Stdout}, e, o)
}

// runUninstall plans what the chosen action would remove, shows the plan and,
// after confirmation, runs it.
func runUninstall(u setupUI, e uninstallEnv, o uninstallOpts) error {
	mode, err := e.chooseMode(u, o)
	if err != nil || mode == modeNone {
		return err
	}
	p := &planner{}
	if !e.plan(u, o, p, mode) {
		fmt.Fprintln(u.out, "\nNo se ha cambiado nada.")
		return nil
	}
	if len(p.steps) == 0 {
		fmt.Fprintln(u.out)
		for _, n := range p.notes {
			printItem(u.out, n.st, n.text)
		}
		fmt.Fprintln(u.out, "\nNo hay nada que hacer.")
		if p.hasFailedNote() {
			return errors.New("algunos pasos no se pudieron preparar; consulta los avisos")
		}
		return nil
	}
	p.print(u.out)
	if o.dryRun {
		fmt.Fprintln(u.out, "\nSimulación (--dry-run): no se ha cambiado nada.")
		return nil
	}
	if !o.yes && !u.yes("¿Continuar? [s/N]") {
		fmt.Fprintln(u.out, "\nCancelado: no se ha cambiado nada.")
		return nil
	}
	return p.run(u.out)
}

// The three actions of `uninstall`.
const (
	modeNone    = 0 // the user chose nothing, or the flags are unusable
	modeClients = 1 // disconnect assistants
	modeLogout  = 2 // delete the token
	modeAll     = 3 // remove everything
)

// chooseMode takes the action from the flags or, in a terminal, from the menu.
// A mode of modeNone with a nil error means the user chose nothing and was told so.
func (e uninstallEnv) chooseMode(u setupUI, o uninstallOpts) (int, error) {
	switch {
	case o.clientsSet:
		return modeClients, e.needConfirmation(o)
	case o.logout:
		return modeLogout, e.needConfirmation(o)
	case o.all:
		return modeAll, e.needConfirmation(o)
	}
	if !e.interactive {
		fmt.Fprint(u.out, uninstallHelp())
		return modeNone, errors.New("sin terminal interactiva hay que indicar --clients, --logout o --all; no se ha cambiado nada")
	}
	mode := askMenu(u)
	if mode == modeNone {
		fmt.Fprintln(u.out, "\nNo se ha cambiado nada.")
	}
	return mode, nil
}

// needConfirmation fails when the plan could not be confirmed: there is no
// terminal to ask on, and neither --yes nor --dry-run was given.
func (e uninstallEnv) needConfirmation(o uninstallOpts) error {
	if !e.interactive && !o.yes && !o.dryRun {
		return errors.New("sin terminal no se puede pedir confirmación; añade --yes para continuar o --dry-run para ver el plan")
	}
	return nil
}

// plan fills p for the chosen mode. It reports false when the user chose no
// assistant to disconnect.
func (e uninstallEnv) plan(u setupUI, o uninstallOpts, p *planner, mode int) bool {
	cls := e.inspectClients()
	switch mode {
	case modeClients:
		sel, ok := selectClients(u, o, cls)
		if !ok {
			return false
		}
		e.planClients(u, o, p, cls, sel)
		if sel == nil && len(p.steps) == 0 && len(p.notes) == 0 {
			p.note(stInfo, "Ningún asistente tiene AulaGlobal conectado.")
		}
		p.conserved = []string{"el programa, tu clave de acceso y tu carpeta AulaGlobal"}
		p.hints = []string{"Reinicia los asistentes afectados para que dejen de ver AulaGlobal."}
	case modeLogout:
		e.planToken(p, cls, false)
		p.conserved = []string{"el programa, las conexiones con tus asistentes y tu carpeta AulaGlobal"}
		p.hints = []string{"Para volver a conectarte, ejecuta `aulaglobal-mcp setup`."}
	case modeAll:
		e.planAll(u, o, p, cls)
	}
	return true
}

func askMenu(u setupUI) int {
	fmt.Fprint(u.out, `¿Qué quieres hacer?
  1. Desconectar AulaGlobal de algunos asistentes
  2. Cerrar la sesión de AulaGlobal (borrar la clave de acceso)
  3. Desinstalar todo
Opción [1]: `)
	for attempt := 0; attempt < 3; attempt++ {
		switch strings.TrimSpace(u.line()) {
		case "", "1":
			return modeClients
		case "2":
			return modeLogout
		case "3":
			return modeAll
		}
		fmt.Fprint(u.out, "! Escribe 1, 2 o 3.\nOpción [1]: ")
	}
	return modeNone
}

// selectClients returns the set of assistant IDs to disconnect (nil = all that
// are connected). ok is false when the user chose none.
func selectClients(u setupUI, o uninstallOpts, cls []clientLink) (map[string]bool, bool) {
	if o.clientsSet {
		sel := map[string]bool{}
		for _, id := range strings.Split(o.clients, ",") {
			sel[strings.TrimSpace(id)] = true
		}
		return sel, true
	}
	var present []clientLink
	for _, c := range cls {
		if c.l.state == linkPresent {
			present = append(present, c)
		}
	}
	if len(present) == 0 {
		return nil, true // the plan explains why there is nothing to do
	}
	fmt.Fprintln(u.out, "\nAsistentes con AulaGlobal conectado:")
	for i, c := range present {
		extra := ""
		if !c.ours {
			extra = " (la entrada apunta a " + quote(c.l.command) + ", no a este programa)"
		}
		fmt.Fprintf(u.out, "  %d. %s%s\n", i+1, c.a.Name(), extra)
	}
	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprint(u.out, "\n› ¿De cuáles? Números separados por comas, Enter para todos, 0 para ninguno: ")
		answer := strings.ToLower(strings.TrimSpace(u.line()))
		switch answer {
		case "", "todos", "t":
			return nil, true
		case "0", "ninguno", "n":
			return nil, false
		}
		sel := map[string]bool{}
		valid := true
		for _, f := range strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' }) {
			n, err := strconv.Atoi(f)
			if err != nil || n < 1 || n > len(present) {
				valid = false
				break
			}
			sel[present[n-1].a.ID()] = true
		}
		if valid {
			return sel, true
		}
		fmt.Fprintln(u.out, "! No te he entendido; usa números de la lista.")
	}
	return nil, false
}
