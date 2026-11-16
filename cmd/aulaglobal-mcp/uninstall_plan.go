package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// An uninstall is first planned and then run. Planning only reads: each
// planX method inspects the computer and adds steps (what will be done, and a
// function that does it) and notes (what will not be done, and why) to a
// planner. Nothing changes until the user has seen the plan and confirmed.

// stepStatus is the result of one step or note: ✓, ·, ! or ✗.
type stepStatus int

const (
	stOK stepStatus = iota
	stInfo
	stWarn
	stFail
)

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

func (p *planner) hasFailedNote() bool {
	for _, n := range p.notes {
		if n.st == stFail {
			return true
		}
	}
	return false
}

// print shows what the plan will do, what it keeps and what it cannot do.
func (p *planner) print(out io.Writer) {
	fmt.Fprintln(out, "\nPlan:")
	for _, s := range p.steps {
		fmt.Fprintln(out, "  · "+s.plan)
	}
	if len(p.conserved) > 0 {
		fmt.Fprintln(out, "\nSe conservará: "+strings.Join(p.conserved, "; ")+".")
	}
	if len(p.notes) > 0 {
		fmt.Fprintln(out, "\nAvisos:")
		for _, n := range p.notes {
			printItem(out, n.st, n.text)
		}
	}
}

// run executes the steps in order and reports each result. It returns an error
// if a step failed or a note already said something could not be prepared.
func (p *planner) run(out io.Writer) error {
	failed := p.hasFailedNote()
	fmt.Fprintln(out, "\nResultado:")
	for _, s := range p.steps {
		st, text := s.run()
		failed = failed || st == stFail
		printItem(out, st, text)
	}
	if failed {
		return errors.New("algunos pasos no se pudieron completar; consulta el resumen")
	}
	fmt.Fprintln(out)
	for _, h := range p.hints {
		fmt.Fprintln(out, h)
	}
	return nil
}
