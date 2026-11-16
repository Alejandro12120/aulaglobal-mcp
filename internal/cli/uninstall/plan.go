package uninstall

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/termui"
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
	description string
	run         func() (stepStatus, string)
}

type note struct {
	status stepStatus
	text   string
}

type planner struct {
	steps     []step
	notes     []note
	conserved []string
	hints     []string
	pathEdit  bool // a PATH entry or profile line is being removed
}

func (plan *planner) add(description string, run func() (stepStatus, string)) {
	plan.steps = append(plan.steps, step{description, run})
}

func (plan *planner) note(status stepStatus, text string) {
	plan.notes = append(plan.notes, note{status, text})
}

func marker(status stepStatus) string {
	return map[stepStatus]string{stOK: "✓", stInfo: "·", stWarn: "!", stFail: "✗"}[status]
}

// printItem prints one ✓/·/!/✗ line; continuation lines are indented under it.
func printItem(out io.Writer, status stepStatus, text string) {
	lines := strings.Split(text, "\n")
	fmt.Fprintln(out, "  "+marker(status)+" "+lines[0])
	for _, line := range lines[1:] {
		fmt.Fprintln(out, "    "+line)
	}
}

type clientLink struct {
	assistant assistants.Assistant
	link      assistants.Link
	ours      bool
}

func (e uninstallEnv) inspectClients() []clientLink {
	var out []clientLink
	for _, assistant := range e.assistantList {
		link := assistant.Find(e.assistantEnv)
		out = append(out, clientLink{assistant, link, link.State == assistants.LinkPresent && pointsToProgram(link.Command, e.exe, e.goos == "windows")})
	}
	return out
}

func quote(s string) string { return "«" + s + "»" }

// planClients adds one step per assistant to disconnect. A nil sel means every
// assistant that has the entry. Otherwise only the IDs in sel are considered,
// and an assistant named there that has nothing to remove gets a note.
func (e uninstallEnv) planClients(ui termui.UI, opts uninstallOpts, plan *planner, links []clientLink, selected map[string]bool) {
	explicit := selected != nil
	for _, client := range links {
		if explicit && !selected[client.assistant.ID()] {
			continue
		}
		name := client.assistant.Name()
		switch client.link.State {
		case assistants.LinkNotInstalled:
			if explicit {
				plan.note(stInfo, name+": no está instalado en este ordenador.")
			}
		case assistants.LinkNone:
			if explicit {
				plan.note(stInfo, name+": no tiene AulaGlobal conectado.")
			}
		case assistants.LinkElsewhere:
			plan.note(stWarn, name+": AulaGlobal "+client.link.Detail)
		case assistants.LinkError:
			plan.note(stFail, name+": no se puede modificar con seguridad ("+client.link.Detail+"). No se ha tocado nada.\n"+client.assistant.ManualRemove(e.assistantEnv))
		case assistants.LinkPresent:
			if !client.ours {
				if !e.interactive || opts.yes || !ui.Yes(name+": la entrada «aulaglobal» apunta a "+quote(client.link.Command)+", no a este programa. ¿Quitarla igualmente? [s/N]") {
					plan.note(stWarn, name+": la entrada apunta a "+quote(client.link.Command)+", no a este programa; no se toca.")
					continue
				}
			}
			client := client
			description := name + ": ejecutar `" + client.link.Detail + "`"
			if client.link.Location != "" {
				description = name + ": quitar la entrada «aulaglobal» de " + client.link.Location + " (copia de seguridad: " + client.link.Location + ".bak)"
			}
			plan.add(description, func() (stepStatus, string) {
				if err := client.assistant.Remove(e.assistantEnv); err != nil {
					return stFail, name + ": no se pudo desconectar con seguridad. No se ha tocado nada más.\n" + client.assistant.ManualRemove(e.assistantEnv)
				}
				return stOK, name + ": desconectado."
			})
		}
	}
}

// tokenCandidates is the token path from flags/env/defaults plus any custom
// path an assistant entry of ours points at (only if it looks like a token).
func (e uninstallEnv) tokenCandidates(links []clientLink) []string {
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		if path = filepath.Clean(path); !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	add(e.tokenFile)
	for _, client := range links {
		if path := client.link.Env["AULAGLOBAL_TOKEN_FILE"]; client.ours && filepath.IsAbs(path) {
			if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && info.Size() <= 64<<10 {
				add(path)
			}
		}
	}
	return out
}

func isDir(path string) bool { info, err := os.Lstat(path); return err == nil && info.IsDir() }

// planToken deletes the token files and, once empty, their folder. With
// wholeConfig the program's config folder is skipped here, because planAll
// deletes it as a whole.
func (e uninstallEnv) planToken(plan *planner, links []clientLink, wholeConfig bool) {
	dirs := map[string]bool{}
	var order []string
	found := false
	for _, tokenPath := range e.tokenCandidates(links) {
		if dir := filepath.Dir(tokenPath); !dirs[dir] {
			dirs[dir] = true
			order = append(order, dir)
		}
		for _, file := range tokenFiles(tokenPath) {
			found = true
			file := file
			plan.add("Borrar "+file, func() (stepStatus, string) {
				if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
					return stFail, "No se pudo borrar " + file + ". Bórralo tú."
				}
				return stOK, "Borrado: " + file
			})
		}
	}
	if !found {
		plan.note(stInfo, "No hay ninguna clave de acceso guardada ("+e.tokenFile+").")
	}
	for _, dir := range order {
		if !isDir(dir) || (wholeConfig && sameDir(dir, e.configDir)) || sameDir(dir, e.home) || filepath.Dir(dir) == dir {
			continue
		}
		dir := dir
		plan.add("Borrar la carpeta "+dir+" si queda vacía", func() (stepStatus, string) {
			if err := os.Remove(dir); err != nil {
				return stInfo, "La carpeta " + dir + " se conserva (no está vacía)."
			}
			return stOK, "Carpeta borrada: " + dir
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

func (e uninstallEnv) planAll(ui termui.UI, opts uninstallOpts, plan *planner, links []clientLink) {
	e.planClients(ui, opts, plan, links, nil)
	e.planToken(plan, links, true)
	if isDir(e.configDir) {
		plan.add("Borrar la carpeta de configuración "+e.configDir, removeOwnDir(e.configDir, "la carpeta de configuración"))
	}
	if isDir(e.cacheDir) {
		plan.add("Borrar la caché de avisos de versión "+e.cacheDir, removeOwnDir(e.cacheDir, "la caché"))
	}
	exeDir := filepath.Dir(e.exe)
	if e.inContainer {
		plan.note(stWarn, "Se está ejecutando en un contenedor: el programa no se borra desde dentro. Elimina el contenedor o la imagen con Docker.")
	} else if e.goos == "windows" {
		e.planWindowsPath(plan, exeDir)
	} else {
		e.planProfiles(plan, exeDir)
	}
	e.planData(ui, opts, plan, links)
	if !e.inContainer {
		rmDir := baseName(exeDir) == programName
		description := "Borrar el programa " + e.exe
		if e.goos == "windows" {
			description = "Apartar el programa como " + e.exe + ".old y borrarlo en cuanto termine este comando"
		}
		if rmDir {
			description += " y su carpeta " + exeDir + " si queda vacía"
		}
		plan.add(description, func() (stepStatus, string) { return e.removeBinary(e.exe, rmDir) })
	}
	if e.inContainer {
		plan.hints = append(plan.hints, "Se han quitado las conexiones y la clave; el programa sigue dentro de este contenedor.")
	} else {
		plan.hints = append(plan.hints, "AulaGlobal MCP se ha desinstalado de este ordenador.")
	}
	if plan.pathEdit {
		plan.hints = append(plan.hints, "Abre una terminal nueva para que el PATH se actualice.")
	}
}

func (e uninstallEnv) planProfiles(plan *planner, exeDir string) {
	for _, file := range profileCandidates(e.home, e.zdotdir, e.xdg) {
		content, err := os.ReadFile(file)
		if err != nil || !strings.Contains(string(content), pathMarker) {
			continue
		}
		match := func(dir string) bool { return sameDir(dir, exeDir) }
		_, removed, others := removeMarkedPathLines(string(content), e.home, match)
		for _, dir := range others {
			plan.note(stInfo, file+": la línea marcada «"+pathMarker+"» de "+dir+" es de otra carpeta; no se toca.")
		}
		if removed == 0 {
			continue
		}
		plan.pathEdit = true
		file := file
		plan.add("Quitar de "+file+" la línea que añade "+exeDir+" al PATH (copia de seguridad: "+file+".bak)", func() (stepStatus, string) {
			current, err := os.ReadFile(file)
			if err != nil {
				return stFail, "No se pudo leer " + file + ". Quita tú la línea marcada «" + pathMarker + "»."
			}
			out, removed, _ := removeMarkedPathLines(string(current), e.home, match)
			if removed == 0 {
				return stInfo, file + ": la línea ya no estaba."
			}
			if err = rewriteProfile(file, []byte(out)); err != nil {
				return stFail, "No se pudo modificar " + file + ". Quita tú la línea marcada «" + pathMarker + "»."
			}
			return stOK, "PATH: línea quitada de " + file + " (copia en " + file + ".bak)"
		})
	}
}

func (e uninstallEnv) planWindowsPath(plan *planner, exeDir string) {
	if e.winPath == nil {
		return
	}
	pathValue, err := e.winPath.Get()
	if err != nil {
		plan.note(stWarn, "No se pudo leer el PATH de tu usuario. Si hace falta, quita tú "+exeDir+" de las variables de entorno.")
		return
	}
	if _, found := removePathEntry(pathValue, exeDir, e.getenv); !found {
		return
	}
	plan.pathEdit = true
	plan.add("Quitar "+exeDir+" del PATH de tu usuario (solo esa entrada)", func() (stepStatus, string) {
		current, err := e.winPath.Get()
		if err != nil {
			return stFail, "No se pudo leer el PATH de tu usuario. Quita tú " + exeDir + " de las variables de entorno."
		}
		out, found := removePathEntry(current, exeDir, e.getenv)
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
func (e uninstallEnv) dataCandidates(links []clientLink) []string {
	seen := map[string]bool{}
	var out []string
	add := func(dir string) {
		if dir = filepath.Clean(dir); !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	for _, client := range links {
		if client.ours {
			if dir := client.link.Env["AULAGLOBAL_DOWNLOAD_DIR"]; dir != "" {
				add(dir)
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
func (e uninstallEnv) planData(ui termui.UI, opts uninstallOpts, plan *planner, links []clientLink) {
	protect := []string{filepath.Dir(e.exe), e.configDir, e.cacheDir, filepath.Dir(e.tokenFile)}
	for _, dir := range e.dataCandidates(links) {
		pathValue := checkDataDir(dir, e.home, e.dataDefault, protect)
		switch {
		case pathValue.reason != "":
			plan.note(stWarn, "No se borrará "+pathValue.dirName+" ("+pathValue.reason+"). Si de verdad quieres borrarla, hazlo tú a mano.")
			continue
		case !pathValue.exists:
			continue
		}
		// A custom folder may hold more than the downloads: only an interactive
		// answer to an explicit warning can delete it, never a flag.
		ask := e.interactive && !opts.yes
		want := false
		switch {
		case pathValue.custom && !ask:
			plan.note(stInfo, "Se conserva "+dir+" (no es la carpeta predeterminada; solo se borra respondiendo a la pregunta en una terminal).")
		case opts.deleteData && !pathValue.custom:
			want = true
		case ask:
			if pathValue.custom {
				fmt.Fprintln(ui.Out, "\n! "+dir+" no es la carpeta predeterminada: se borrará todo lo que haya dentro, no solo los apuntes.")
			}
			want = ui.Yes("¿Borrar también la carpeta AulaGlobal con tus materiales (" + dir + ")? [s/N]")
		}
		if !want {
			if !(pathValue.custom && !ask) {
				plan.conserved = append(plan.conserved, "tu carpeta AulaGlobal con tus materiales ("+dir+")")
			}
			continue
		}
		dir := dir
		plan.add("Borrar la carpeta de apuntes "+dir+" y todo lo que contiene", func() (stepStatus, string) {
			current := checkDataDir(dir, e.home, e.dataDefault, protect)
			if !current.ok {
				return stWarn, "No se borra " + dir + " (" + current.reason + ")."
			}
			var err error
			if current.empty {
				err = os.Remove(dir)
			} else {
				err = os.RemoveAll(dir)
			}
			if err != nil {
				return stFail, "No se pudo borrar " + dir + " por completo. Bórrala tú."
			}
			return stOK, "Carpeta de apuntes borrada: " + dir
		})
	}
}

func (plan *planner) hasFailedNote() bool {
	for _, item := range plan.notes {
		if item.status == stFail {
			return true
		}
	}
	return false
}

// print shows what the plan will do, what it keeps and what it cannot do.
func (plan *planner) print(out io.Writer) {
	fmt.Fprintln(out, "\nPlan:")
	for _, step := range plan.steps {
		fmt.Fprintln(out, "  · "+step.description)
	}
	if len(plan.conserved) > 0 {
		fmt.Fprintln(out, "\nSe conservará: "+strings.Join(plan.conserved, "; ")+".")
	}
	if len(plan.notes) > 0 {
		fmt.Fprintln(out, "\nAvisos:")
		for _, item := range plan.notes {
			printItem(out, item.status, item.text)
		}
	}
}

// run executes the steps in order and reports each result. It returns an error
// if a step failed or a note already said something could not be prepared.
func (plan *planner) run(out io.Writer) error {
	failed := plan.hasFailedNote()
	fmt.Fprintln(out, "\nResultado:")
	for _, step := range plan.steps {
		status, text := step.run()
		failed = failed || status == stFail
		printItem(out, status, text)
	}
	if failed {
		return errors.New("algunos pasos no se pudieron completar; consulta el resumen")
	}
	fmt.Fprintln(out)
	for _, h := range plan.hints {
		fmt.Fprintln(out, h)
	}
	return nil
}
