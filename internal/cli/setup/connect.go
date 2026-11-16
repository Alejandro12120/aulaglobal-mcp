package setup

import (
	"fmt"
	"strings"

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/termui"
)

// privacyNotice is shown before the assistants are connected. It says what
// leaves this computer, where tool results end up, and that the assistant may
// be able to reach other files.
const privacyNotice = `· Privacidad:
  - Este programa solo envía tus datos a AulaGlobal; el autor no recibe nada.
  - Lo que el asistente consulte (asignaturas, apuntes, entregas) pasa a formar
    parte de la conversación y llega a la empresa del asistente (Anthropic,
    OpenAI, Google...), igual que tus mensajes.
  - Este programa solo usa la carpeta AulaGlobal, pero el asistente puede tener
    permiso para abrir otros archivos de tu ordenador.
`

// connectAssistants is step 4 of setup: the privacy notice, then detection and
// configuration. It reports whether any assistant failed.
func connectAssistants(ui termui.UI, e assistants.Env, want assistants.Entry, selected []assistants.Assistant, yes bool) bool {
	step(ui, 4, "Conectar con tus asistentes")
	fmt.Fprint(ui.Out, privacyNotice+"\n")
	return configureClients(ui, e, want, selected, yes)
}

// foundClient is an assistant detected on this computer, with what setup found
// in its configuration and what it ended up doing.
type foundClient struct {
	a       assistants.Assistant
	i       assistants.Inspection
	outcome string
	changed bool
}

func (c *foundClient) needsChange() bool { return c.i.State != assistants.Identical && c.i.Err == nil }

// configureClients detects the assistants in list and adds or updates the
// aulaglobal entry in each one the user agrees to. It reports whether any
// assistant failed.
func configureClients(ui termui.UI, e assistants.Env, w assistants.Entry, list []assistants.Assistant, yes bool) bool {
	clients := listFoundClients(ui, e, w, list)
	all := yes
	if !yes && countNeedingChange(clients) >= 2 {
		all = askConfigureAll(ui, clients)
	}
	failed := false
	for _, c := range clients {
		fmt.Fprintln(ui.Out)
		if c.configure(ui, e, w, all) {
			failed = true
		}
	}
	fmt.Fprintln(ui.Out, "\nResumen:")
	for _, c := range clients {
		fmt.Fprintln(ui.Out, "  "+c.a.Name()+": "+c.outcome)
	}
	changed := printReloadHints(ui, clients)
	if changed {
		fmt.Fprintln(ui.Out, "\nPrueba a pedir: «¿Qué asignaturas tengo?»")
	} else if !failed {
		fmt.Fprintln(ui.Out, "\nNo se ha cambiado ninguna configuración.")
	}
	return failed
}

func listFoundClients(ui termui.UI, e assistants.Env, w assistants.Entry, list []assistants.Assistant) []*foundClient {
	var clients []*foundClient
	var absent []string
	fmt.Fprintln(ui.Out, "Asistentes encontrados en este ordenador:")
	for _, a := range list {
		if !a.Detect(e) {
			absent = append(absent, a.Name())
			continue
		}
		clients = append(clients, &foundClient{a: a, i: a.Inspect(e, w)})
		fmt.Fprintln(ui.Out, "  ✓ "+a.Name())
	}
	if len(clients) == 0 {
		fmt.Fprintln(ui.Out, "  Ninguno.")
	}
	if len(absent) > 0 {
		fmt.Fprintln(ui.Out, "No encontrados: "+strings.Join(absent, ", "))
	}
	return clients
}

func countNeedingChange(clients []*foundClient) int {
	n := 0
	for _, c := range clients {
		if c.needsChange() {
			n++
		}
	}
	return n
}

// askConfigureAll asks once for all assistants. Answering "n" skips every
// assistant that needs a change; anything else that is not yes asks one by one.
func askConfigureAll(ui termui.UI, clients []*foundClient) bool {
	fmt.Fprint(ui.Out, "\n› ¿Configurar todos? [S/n/e=elegir uno a uno] ")
	answer := strings.ToLower(strings.TrimSpace(ui.Line()))
	fmt.Fprintln(ui.Out)
	if answer == "n" {
		for _, c := range clients {
			if c.needsChange() {
				c.outcome = "omitido"
			}
		}
	}
	return answer == "" || termui.AcceptsYes(answer)
}

// configure explains what it found, asks (unless all is set) and applies the
// change. It reports whether the assistant could not be configured.
func (c *foundClient) configure(ui termui.UI, e assistants.Env, w assistants.Entry, all bool) (failed bool) {
	a, i := c.a, c.i
	fail := func() bool {
		c.outcome = "falló (ver instrucciones arriba)"
		fmt.Fprintln(ui.Out, "✗ "+a.Name()+": no se pudo configurar con seguridad.")
		fmt.Fprintln(ui.Out, a.Manual(w))
		return true
	}
	if i.Err != nil {
		return fail()
	}
	if i.State == assistants.Identical {
		c.outcome = "ya estaba"
		fmt.Fprintln(ui.Out, "✓ "+a.Name()+": ya está configurado.")
		return false
	}
	q := "¿Añadir AulaGlobal a " + a.Name() + "?"
	if i.State == assistants.Different {
		fmt.Fprintln(ui.Out, "! "+a.Name()+": configuración diferente ("+i.Detail+").")
		fmt.Fprintln(ui.Out, "  Se usará: "+w.Exe+" --transport stdio")
		q = "¿Actualizarlo?"
	} else if i.State == assistants.OtherScope {
		fmt.Fprintln(ui.Out, "! "+a.Name()+": "+i.Detail)
		if i.Scope == "local" {
			q = "¿Mover AulaGlobal al ámbito de usuario?"
		} else {
			q = "¿Añadir también AulaGlobal al ámbito de usuario?"
		}
	}
	if c.outcome == "omitido" || !ui.Confirm(q, all) {
		c.outcome = "omitido"
		fmt.Fprintln(ui.Out, "· "+a.Name()+": omitido.")
		return false
	}
	if err := a.Apply(e, w, i.State != assistants.Missing); err != nil {
		return fail()
	}
	c.changed = true
	c.outcome = "añadido"
	if i.State != assistants.Missing {
		c.outcome = "actualizado"
	}
	fmt.Fprintln(ui.Out, "✓ "+a.Name()+": "+c.outcome+".")
	return false
}

// printReloadHints tells the user how to make each changed assistant pick up
// the new entry. It reports whether any assistant was changed.
func printReloadHints(ui termui.UI, clients []*foundClient) bool {
	changed := false
	for _, c := range clients {
		if c.changed {
			if !changed {
				fmt.Fprintln(ui.Out)
			}
			changed = true
			fmt.Fprintln(ui.Out, c.a.Name()+": "+c.a.Reload())
		}
	}
	return changed
}
