package uninstall

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

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/cli/env"
	"aulaglobal-mcp/internal/selfupdate"
	"aulaglobal-mcp/internal/termui"
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

func uninstallHelp() string { return fmt.Sprintf(uninstallUsage, assistants.IDs()) }

type uninstallOpts struct {
	clients                 string
	clientsSet              bool
	logout, all             bool
	deleteData, yes, dryRun bool
	secretsDir, dataDir     string
}

func parseUninstallFlags(args []string) (uninstallOpts, error) {
	var opts uninstallOpts
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.clients, "clients", "", "")
	flags.BoolVar(&opts.logout, "logout", false, "")
	flags.BoolVar(&opts.all, "all", false, "")
	flags.BoolVar(&opts.deleteData, "delete-data", false, "")
	flags.BoolVar(&opts.yes, "yes", false, "")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "")
	flags.StringVar(&opts.secretsDir, "secrets-dir", "", "")
	flags.StringVar(&opts.dataDir, "data-dir", "", "")
	if err := flags.Parse(args); err != nil {
		return opts, err
	}
	flags.Visit(func(visited *flag.Flag) { opts.clientsSet = opts.clientsSet || visited.Name == "clients" })
	if flags.NArg() > 0 {
		return opts, fmt.Errorf("argumento inesperado %q", flags.Arg(0))
	}
	chosen := 0
	for _, isSet := range []bool{opts.clientsSet, opts.logout, opts.all} {
		if isSet {
			chosen++
		}
	}
	switch {
	case chosen > 1:
		return opts, errors.New("usa solo una de las opciones --clients, --logout y --all")
	case opts.deleteData && !opts.all:
		return opts, errors.New("--delete-data solo se puede usar junto con --all")
	case opts.clientsSet && strings.TrimSpace(opts.clients) == "":
		return opts, errors.New("--clients necesita al menos un asistente: " + assistants.IDs())
	}
	if opts.clientsSet {
		if _, err := assistants.Select(opts.clients); err != nil {
			return opts, err
		}
	}
	return opts, nil
}

// uninstallEnv is everything uninstall touches, injected so tests run in a
// temporary directory with fake paths, a fake registry and a fake executable.
type uninstallEnv struct {
	assistantEnv  assistants.Env
	assistantList []assistants.Assistant
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
func Run(args []string) error {
	opts, err := parseUninstallFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Print(uninstallHelp())
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w\n\n%s", err, uninstallHelp())
	}
	tokenFile, defaultData, err := userpaths.Defaults()
	if err != nil {
		return err
	}
	tokenFile = env.Or("AULAGLOBAL_TOKEN_FILE", tokenFile)
	if opts.secretsDir != "" {
		tokenFile = filepath.Join(opts.secretsDir, "token")
	}
	if tokenFile, err = filepath.Abs(tokenFile); err != nil {
		return err
	}
	extraDataDir := env.Or("AULAGLOBAL_DOWNLOAD_DIR", "")
	if opts.dataDir != "" {
		extraDataDir = opts.dataDir
	}
	if extraDataDir != "" {
		if extraDataDir, err = filepath.Abs(extraDataDir); err != nil {
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
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	cachePath, err := userpaths.UpdateCache()
	if err != nil {
		return err
	}
	machine := uninstallEnv{
		assistantEnv: assistants.RealEnv(runtime.GOOS, home, configDir), assistantList: assistants.All(), goos: runtime.GOOS, home: home, exe: exe,
		tokenFile: tokenFile, configDir: filepath.Join(configDir, programName), cacheDir: filepath.Dir(cachePath),
		dataDefault: defaultData, dataExtra: extraDataDir, zdotdir: os.Getenv("ZDOTDIR"), xdg: os.Getenv("XDG_CONFIG_HOME"),
		inContainer: selfupdate.InContainer(), interactive: term.IsTerminal(int(os.Stdin.Fd())),
		winPath: realPathStore(), getenv: os.Getenv, afterPathEdit: broadcastEnvironmentChange,
	}
	if runtime.GOOS == "windows" {
		machine.removeBinary = func(exe string, rmDir bool) (stepStatus, string) {
			return removeBinaryWindows(exe, rmDir, spawnDetached)
		}
	} else {
		machine.removeBinary = removeBinaryUnix
	}
	return runUninstall(termui.UI{In: os.Stdin, Out: os.Stdout}, machine, opts)
}

// runUninstall plans what the chosen action would remove, shows the plan and,
// after confirmation, runs it.
func runUninstall(ui termui.UI, e uninstallEnv, opts uninstallOpts) error {
	mode, err := e.chooseMode(ui, opts)
	if err != nil || mode == modeNone {
		return err
	}
	plan := &planner{}
	if !e.plan(ui, opts, plan, mode) {
		fmt.Fprintln(ui.Out, "\nNo se ha cambiado nada.")
		return nil
	}
	if len(plan.steps) == 0 {
		fmt.Fprintln(ui.Out)
		for _, item := range plan.notes {
			printItem(ui.Out, item.status, item.text)
		}
		fmt.Fprintln(ui.Out, "\nNo hay nada que hacer.")
		if plan.hasFailedNote() {
			return errors.New("algunos pasos no se pudieron preparar; consulta los avisos")
		}
		return nil
	}
	plan.print(ui.Out)
	if opts.dryRun {
		fmt.Fprintln(ui.Out, "\nSimulación (--dry-run): no se ha cambiado nada.")
		return nil
	}
	if !opts.yes && !ui.Yes("¿Continuar? [s/N]") {
		fmt.Fprintln(ui.Out, "\nCancelado: no se ha cambiado nada.")
		return nil
	}
	return plan.run(ui.Out)
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
func (e uninstallEnv) chooseMode(ui termui.UI, opts uninstallOpts) (int, error) {
	switch {
	case opts.clientsSet:
		return modeClients, e.needConfirmation(opts)
	case opts.logout:
		return modeLogout, e.needConfirmation(opts)
	case opts.all:
		return modeAll, e.needConfirmation(opts)
	}
	if !e.interactive {
		fmt.Fprint(ui.Out, uninstallHelp())
		return modeNone, errors.New("sin terminal interactiva hay que indicar --clients, --logout o --all; no se ha cambiado nada")
	}
	mode := askMenu(ui)
	if mode == modeNone {
		fmt.Fprintln(ui.Out, "\nNo se ha cambiado nada.")
	}
	return mode, nil
}

// needConfirmation fails when the plan could not be confirmed: there is no
// terminal to ask on, and neither --yes nor --dry-run was given.
func (e uninstallEnv) needConfirmation(opts uninstallOpts) error {
	if !e.interactive && !opts.yes && !opts.dryRun {
		return errors.New("sin terminal no se puede pedir confirmación; añade --yes para continuar o --dry-run para ver el plan")
	}
	return nil
}

// plan fills p for the chosen mode. It reports false when the user chose no
// assistant to disconnect.
func (e uninstallEnv) plan(ui termui.UI, opts uninstallOpts, plan *planner, mode int) bool {
	links := e.inspectClients()
	switch mode {
	case modeClients:
		selected, ok := selectClients(ui, opts, links)
		if !ok {
			return false
		}
		e.planClients(ui, opts, plan, links, selected)
		if selected == nil && len(plan.steps) == 0 && len(plan.notes) == 0 {
			plan.note(stInfo, "Ningún asistente tiene AulaGlobal conectado.")
		}
		plan.conserved = []string{"el programa, tu clave de acceso y tu carpeta AulaGlobal"}
		plan.hints = []string{"Reinicia los asistentes afectados para que dejen de ver AulaGlobal."}
	case modeLogout:
		e.planToken(plan, links, false)
		plan.conserved = []string{"el programa, las conexiones con tus asistentes y tu carpeta AulaGlobal"}
		plan.hints = []string{"Para volver a conectarte, ejecuta `aulaglobal-mcp setup`."}
	case modeAll:
		e.planAll(ui, opts, plan, links)
	}
	return true
}

func askMenu(ui termui.UI) int {
	fmt.Fprint(ui.Out, `¿Qué quieres hacer?
  1. Desconectar AulaGlobal de algunos asistentes
  2. Cerrar la sesión de AulaGlobal (borrar la clave de acceso)
  3. Desinstalar todo
Opción [1]: `)
	for attempt := 0; attempt < 3; attempt++ {
		switch strings.TrimSpace(ui.Line()) {
		case "", "1":
			return modeClients
		case "2":
			return modeLogout
		case "3":
			return modeAll
		}
		fmt.Fprint(ui.Out, "! Escribe 1, 2 o 3.\nOpción [1]: ")
	}
	return modeNone
}

// selectClients returns the IDs of the assistants to disconnect, or nil for all
// that are connected. ok is false when the user chose none.
func selectClients(ui termui.UI, opts uninstallOpts, links []clientLink) (map[string]bool, bool) {
	if opts.clientsSet {
		selected := map[string]bool{}
		for _, id := range strings.Split(opts.clients, ",") {
			selected[strings.TrimSpace(id)] = true
		}
		return selected, true
	}
	var present []clientLink
	for _, client := range links {
		if client.link.State == assistants.LinkPresent {
			present = append(present, client)
		}
	}
	if len(present) == 0 {
		return nil, true // the plan explains why there is nothing to do
	}
	fmt.Fprintln(ui.Out, "\nAsistentes con AulaGlobal conectado:")
	for i, client := range present {
		extraDataDir := ""
		if !client.ours {
			extraDataDir = " (la entrada apunta a " + quote(client.link.Command) + ", no a este programa)"
		}
		fmt.Fprintf(ui.Out, "  %d. %s%s\n", i+1, client.assistant.Name(), extraDataDir)
	}
	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprint(ui.Out, "\n› ¿De cuáles? Números separados por comas, Enter para todos, 0 para ninguno: ")
		answer := strings.ToLower(strings.TrimSpace(ui.Line()))
		switch answer {
		case "", "todos", "t":
			return nil, true
		case "0", "ninguno", "n":
			return nil, false
		}
		selected := map[string]bool{}
		valid := true
		for _, field := range strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' }) {
			number, err := strconv.Atoi(field)
			if err != nil || number < 1 || number > len(present) {
				valid = false
				break
			}
			selected[present[number-1].assistant.ID()] = true
		}
		if valid {
			return selected, true
		}
		fmt.Fprintln(ui.Out, "! No te he entendido; usa números de la lista.")
	}
	return nil, false
}
