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

// ---- plan ---------------------------------------------------------------------

type step struct {
	plan string
	run  func() (stepStatus, string)
}
type note struct {
	st   stepStatus
	text string
}
type planner struct {
	steps     []step
	notes     []note
	conserved []string
	hints     []string
	pathEdit  bool // a PATH entry or profile line is being removed
}

func (p *planner) add(plan string, run func() (stepStatus, string)) {
	p.steps = append(p.steps, step{plan, run})
}
func (p *planner) note(st stepStatus, text string) { p.notes = append(p.notes, note{st, text}) }

func marker(st stepStatus) string {
	return map[stepStatus]string{stOK: "✓", stInfo: "·", stWarn: "!", stFail: "✗"}[st]
}

// printItem prints one ✓/·/!/✗ line; continuation lines are indented under it.
func printItem(out io.Writer, st stepStatus, text string) {
	lines := strings.Split(text, "\n")
	fmt.Fprintln(out, "  "+marker(st)+" "+lines[0])
	for _, l := range lines[1:] {
		fmt.Fprintln(out, "    "+l)
	}
}

type clientLink struct {
	a    assistant
	l    link
	ours bool
}

func (e uninstallEnv) inspectClients() []clientLink {
	var out []clientLink
	for _, a := range e.list {
		l := a.Find(e.ce)
		out = append(out, clientLink{a, l, l.state == linkPresent && pointsToProgram(l.command, e.exe, e.goos == "windows")})
	}
	return out
}

func quote(s string) string { return "«" + s + "»" }

func runUninstall(u setupUI, e uninstallEnv, o uninstallOpts) error {
	mode := 0
	switch {
	case o.clientsSet:
		mode = 1
	case o.logout:
		mode = 2
	case o.all:
		mode = 3
	}
	if mode == 0 {
		if !e.interactive {
			fmt.Fprint(u.out, uninstallHelp())
			return errors.New("sin terminal interactiva hay que indicar --clients, --logout o --all; no se ha cambiado nada")
		}
		if mode = askMenu(u); mode == 0 {
			fmt.Fprintln(u.out, "\nNo se ha cambiado nada.")
			return nil
		}
	} else if !e.interactive && !o.yes && !o.dryRun {
		return errors.New("sin terminal no se puede pedir confirmación; añade --yes para continuar o --dry-run para ver el plan")
	}

	p := &planner{}
	cls := e.inspectClients()
	switch mode {
	case 1:
		sel, ok := selectClients(u, o, cls)
		if !ok {
			fmt.Fprintln(u.out, "\nNo se ha cambiado nada.")
			return nil
		}
		e.planClients(u, o, p, cls, sel)
		if sel == nil && len(p.steps) == 0 && len(p.notes) == 0 {
			p.note(stInfo, "Ningún asistente tiene AulaGlobal conectado.")
		}
		p.conserved = []string{"el programa, tu clave de acceso y tu carpeta AulaGlobal"}
		p.hints = []string{"Reinicia los asistentes afectados para que dejen de ver AulaGlobal."}
	case 2:
		e.planToken(p, cls, false)
		p.conserved = []string{"el programa, las conexiones con tus asistentes y tu carpeta AulaGlobal"}
		p.hints = []string{"Para volver a conectarte, ejecuta `aulaglobal-mcp setup`."}
	case 3:
		e.planAll(u, o, p, cls)
	}

	failed := false
	for _, n := range p.notes {
		failed = failed || n.st == stFail
	}
	if len(p.steps) == 0 {
		fmt.Fprintln(u.out)
		for _, n := range p.notes {
			printItem(u.out, n.st, n.text)
		}
		fmt.Fprintln(u.out, "\nNo hay nada que hacer.")
		if failed {
			return errors.New("algunos pasos no se pudieron preparar; consulta los avisos")
		}
		return nil
	}

	fmt.Fprintln(u.out, "\nPlan:")
	for _, s := range p.steps {
		fmt.Fprintln(u.out, "  · "+s.plan)
	}
	if len(p.conserved) > 0 {
		fmt.Fprintln(u.out, "\nSe conservará: "+strings.Join(p.conserved, "; ")+".")
	}
	if len(p.notes) > 0 {
		fmt.Fprintln(u.out, "\nAvisos:")
		for _, n := range p.notes {
			printItem(u.out, n.st, n.text)
		}
	}
	if o.dryRun {
		fmt.Fprintln(u.out, "\nSimulación (--dry-run): no se ha cambiado nada.")
		return nil
	}
	if !o.yes {
		if !u.yes("¿Continuar? [s/N]") {
			fmt.Fprintln(u.out, "\nCancelado: no se ha cambiado nada.")
			return nil
		}
	}
	fmt.Fprintln(u.out, "\nResultado:")
	for _, s := range p.steps {
		st, text := s.run()
		failed = failed || st == stFail
		printItem(u.out, st, text)
	}
	if !failed {
		fmt.Fprintln(u.out)
		for _, h := range p.hints {
			fmt.Fprintln(u.out, h)
		}
	}
	if failed {
		return errors.New("algunos pasos no se pudieron completar; consulta el resumen")
	}
	return nil
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
			return 1
		case "2":
			return 2
		case "3":
			return 3
		}
		fmt.Fprint(u.out, "! Escribe 1, 2 o 3.\nOpción [1]: ")
	}
	return 0
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

// planClients adds one step per assistant to disconnect. sel nil = every
// assistant that has the entry; otherwise only the IDs in sel (explicit names
// also get a note when they have nothing to remove).
func (e uninstallEnv) planClients(u setupUI, o uninstallOpts, p *planner, cls []clientLink, sel map[string]bool) {
	explicit := sel != nil
	for _, c := range cls {
		if explicit && !sel[c.a.ID()] {
			continue
		}
		name := c.a.Name()
		switch c.l.state {
		case linkNotInstalled:
			if explicit {
				p.note(stInfo, name+": no está instalado en este ordenador.")
			}
		case linkNone:
			if explicit {
				p.note(stInfo, name+": no tiene AulaGlobal conectado.")
			}
		case linkElsewhere:
			p.note(stWarn, name+": AulaGlobal "+c.l.detail)
		case linkError:
			p.note(stFail, name+": no se puede modificar con seguridad ("+c.l.detail+"). No se ha tocado nada.\n"+c.a.ManualRemove(e.ce))
		case linkPresent:
			if !c.ours {
				if !e.interactive || o.yes || !u.yes(name+": la entrada «aulaglobal» apunta a "+quote(c.l.command)+", no a este programa. ¿Quitarla igualmente? [s/N]") {
					p.note(stWarn, name+": la entrada apunta a "+quote(c.l.command)+", no a este programa; no se toca.")
					continue
				}
			}
			c := c
			plan := name + ": ejecutar `" + c.l.detail + "`"
			if c.l.location != "" {
				plan = name + ": quitar la entrada «aulaglobal» de " + c.l.location + " (copia de seguridad: " + c.l.location + ".bak)"
			}
			p.add(plan, func() (stepStatus, string) {
				if err := c.a.Remove(e.ce); err != nil {
					return stFail, name + ": no se pudo desconectar con seguridad. No se ha tocado nada más.\n" + c.a.ManualRemove(e.ce)
				}
				return stOK, name + ": desconectado."
			})
		}
	}
}

// ---- credentials ------------------------------------------------------------

// tokenCandidates is the token path from flags/env/defaults plus any custom
// path an assistant entry of ours points at (only if it looks like a token).
func (e uninstallEnv) tokenCandidates(cls []clientLink) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p = filepath.Clean(p); !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(e.tokenFile)
	for _, c := range cls {
		if p := c.l.env["AULAGLOBAL_TOKEN_FILE"]; c.ours && filepath.IsAbs(p) {
			if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() && fi.Size() <= 64<<10 {
				add(p)
			}
		}
	}
	return out
}

func isDir(p string) bool { fi, err := os.Lstat(p); return err == nil && fi.IsDir() }

// planToken deletes the token files and, if it is then empty, their folder.
// With wholeConfig the program's own config folder goes entirely.
func (e uninstallEnv) planToken(p *planner, cls []clientLink, wholeConfig bool) {
	dirs := map[string]bool{}
	var order []string
	found := false
	for _, tok := range e.tokenCandidates(cls) {
		if d := filepath.Dir(tok); !dirs[d] {
			dirs[d] = true
			order = append(order, d)
		}
		for _, f := range tokenFiles(tok) {
			found = true
			f := f
			p.add("Borrar "+f, func() (stepStatus, string) {
				if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
					return stFail, "No se pudo borrar " + f + ". Bórralo tú."
				}
				return stOK, "Borrado: " + f
			})
		}
	}
	if !found {
		p.note(stInfo, "No hay ninguna clave de acceso guardada ("+e.tokenFile+").")
	}
	for _, d := range order {
		if !isDir(d) || (wholeConfig && sameDir(d, e.configDir)) || sameDir(d, e.home) || filepath.Dir(d) == d {
			continue
		}
		d := d
		p.add("Borrar la carpeta "+d+" si queda vacía", func() (stepStatus, string) {
			if err := os.Remove(d); err != nil {
				return stInfo, "La carpeta " + d + " se conserva (no está vacía)."
			}
			return stOK, "Carpeta borrada: " + d
		})
	}
}

// removeOwnDir deletes a folder that belongs to this program (its config or
// cache), but only if it is really named after the program.
func removeOwnDir(dir, what string) func() (stepStatus, string) {
	return func() (stepStatus, string) {
		if baseName(dir) != programName {
			return stWarn, "No se borra " + dir + " (no es la carpeta propia del programa)."
		}
		if err := os.RemoveAll(dir); err != nil {
			return stFail, "No se pudo borrar " + what + " " + dir + ". Bórrala tú."
		}
		return stOK, "Borrada " + what + ": " + dir
	}
}

// ---- uninstall everything -----------------------------------------------------

func (e uninstallEnv) planAll(u setupUI, o uninstallOpts, p *planner, cls []clientLink) {
	e.planClients(u, o, p, cls, nil)
	e.planToken(p, cls, true)
	if isDir(e.configDir) {
		p.add("Borrar la carpeta de configuración "+e.configDir, removeOwnDir(e.configDir, "la carpeta de configuración"))
	}
	if isDir(e.cacheDir) {
		p.add("Borrar la caché de avisos de versión "+e.cacheDir, removeOwnDir(e.cacheDir, "la caché"))
	}
	exeDir := filepath.Dir(e.exe)
	if e.inContainer {
		p.note(stWarn, "Se está ejecutando en un contenedor: el programa no se borra desde dentro. Elimina el contenedor o la imagen con Docker.")
	} else if e.goos == "windows" {
		e.planWindowsPath(p, exeDir)
	} else {
		e.planProfiles(p, exeDir)
	}
	e.planData(u, o, p, cls)
	if !e.inContainer {
		rmDir := baseName(exeDir) == programName
		plan := "Borrar el programa " + e.exe
		if e.goos == "windows" {
			plan = "Apartar el programa como " + e.exe + ".old y borrarlo en cuanto termine este comando"
		}
		if rmDir {
			plan += " y su carpeta " + exeDir + " si queda vacía"
		}
		p.add(plan, func() (stepStatus, string) { return e.removeBinary(e.exe, rmDir) })
	}
	if e.inContainer {
		p.hints = append(p.hints, "Se han quitado las conexiones y la clave; el programa sigue dentro de este contenedor.")
	} else {
		p.hints = append(p.hints, "AulaGlobal MCP se ha desinstalado de este ordenador.")
	}
	if p.pathEdit {
		p.hints = append(p.hints, "Abre una terminal nueva para que el PATH se actualice.")
	}
}

func (e uninstallEnv) planProfiles(p *planner, exeDir string) {
	for _, f := range profileCandidates(e.home, e.zdotdir, e.xdg) {
		b, err := os.ReadFile(f)
		if err != nil || !strings.Contains(string(b), pathMarker) {
			continue
		}
		match := func(d string) bool { return sameDir(d, exeDir) }
		_, n, others := removeMarkedPathLines(string(b), e.home, match)
		for _, d := range others {
			p.note(stInfo, f+": la línea marcada «"+pathMarker+"» de "+d+" es de otra carpeta; no se toca.")
		}
		if n == 0 {
			continue
		}
		p.pathEdit = true
		f := f
		p.add("Quitar de "+f+" la línea que añade "+exeDir+" al PATH (copia de seguridad: "+f+".bak)", func() (stepStatus, string) {
			cur, err := os.ReadFile(f)
			if err != nil {
				return stFail, "No se pudo leer " + f + ". Quita tú la línea marcada «" + pathMarker + "»."
			}
			out, n, _ := removeMarkedPathLines(string(cur), e.home, match)
			if n == 0 {
				return stInfo, f + ": la línea ya no estaba."
			}
			if err = rewriteProfile(f, []byte(out)); err != nil {
				return stFail, "No se pudo modificar " + f + ". Quita tú la línea marcada «" + pathMarker + "»."
			}
			return stOK, "PATH: línea quitada de " + f + " (copia en " + f + ".bak)"
		})
	}
}

func (e uninstallEnv) planWindowsPath(p *planner, exeDir string) {
	if e.winPath == nil {
		return
	}
	v, err := e.winPath.Get()
	if err != nil {
		p.note(stWarn, "No se pudo leer el PATH de tu usuario. Si hace falta, quita tú "+exeDir+" de las variables de entorno.")
		return
	}
	if _, found := removePathEntry(v, exeDir, e.getenv); !found {
		return
	}
	p.pathEdit = true
	p.add("Quitar "+exeDir+" del PATH de tu usuario (solo esa entrada)", func() (stepStatus, string) {
		cur, err := e.winPath.Get()
		if err != nil {
			return stFail, "No se pudo leer el PATH de tu usuario. Quita tú " + exeDir + " de las variables de entorno."
		}
		out, found := removePathEntry(cur, exeDir, e.getenv)
		if !found {
			return stInfo, "El PATH del usuario ya no incluía " + exeDir + "."
		}
		if err = e.winPath.Set(out); err != nil {
			return stFail, "No se pudo cambiar el PATH de tu usuario. Quita tú " + exeDir + " de las variables de entorno."
		}
		if e.afterPathEdit != nil {
			e.afterPathEdit()
		}
		return stOK, "PATH del usuario: quitado " + exeDir
	})
}

// dataCandidates are the folders where materials may live: what each assistant
// entry of ours says, --data-dir / AULAGLOBAL_DOWNLOAD_DIR, or the default.
func (e uninstallEnv) dataCandidates(cls []clientLink) []string {
	seen := map[string]bool{}
	var out []string
	add := func(d string) {
		if d = filepath.Clean(d); !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	for _, c := range cls {
		if c.ours {
			if d := c.l.env["AULAGLOBAL_DOWNLOAD_DIR"]; d != "" {
				add(d)
			} else {
				add(e.dataDefault)
			}
		}
	}
	if e.dataExtra != "" {
		add(e.dataExtra)
	}
	if len(out) == 0 {
		add(e.dataDefault)
	}
	return out
}

// planData decides, folder by folder, whether to delete the materials. Nothing
// outside a folder that passes checkDataDir is ever deleted.
func (e uninstallEnv) planData(u setupUI, o uninstallOpts, p *planner, cls []clientLink) {
	protect := []string{filepath.Dir(e.exe), e.configDir, e.cacheDir, filepath.Dir(e.tokenFile)}
	for _, d := range e.dataCandidates(cls) {
		v := checkDataDir(d, e.home, e.dataDefault, protect)
		switch {
		case v.reason != "":
			p.note(stWarn, "No se borrará "+v.dirName+" ("+v.reason+"). Si de verdad quieres borrarla, hazlo tú a mano.")
			continue
		case !v.exists:
			continue
		}
		// A custom folder may hold more than the downloads: only an interactive
		// answer to an explicit warning can delete it, never a flag.
		ask := e.interactive && !o.yes
		want := false
		switch {
		case v.custom && !ask:
			p.note(stInfo, "Se conserva "+d+" (no es la carpeta predeterminada; solo se borra respondiendo a la pregunta en una terminal).")
		case o.deleteData && !v.custom:
			want = true
		case ask:
			if v.custom {
				fmt.Fprintln(u.out, "\n! "+d+" no es la carpeta predeterminada: se borrará todo lo que haya dentro, no solo los apuntes.")
			}
			want = u.yes("¿Borrar también la carpeta AulaGlobal con tus materiales (" + d + ")? [s/N]")
		}
		if !want {
			if !(v.custom && !ask) {
				p.conserved = append(p.conserved, "tu carpeta AulaGlobal con tus materiales ("+d+")")
			}
			continue
		}
		d := d
		p.add("Borrar la carpeta de apuntes "+d+" y todo lo que contiene", func() (stepStatus, string) {
			cur := checkDataDir(d, e.home, e.dataDefault, protect)
			if !cur.ok {
				return stWarn, "No se borra " + d + " (" + cur.reason + ")."
			}
			var err error
			if cur.empty {
				err = os.Remove(d)
			} else {
				err = os.RemoveAll(d)
			}
			if err != nil {
				return stFail, "No se pudo borrar " + d + " por completo. Bórrala tú."
			}
			return stOK, "Carpeta de apuntes borrada: " + d
		})
	}
}
