package main

import (
	"fmt"
	"strings"
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
func connectAssistants(u setupUI, e clientEnv, want desiredEntry, selected []assistant, yes bool) bool {
	u.step(4, "Conectar con tus asistentes")
	fmt.Fprint(u.out, privacyNotice+"\n")
	return configureClients(u, e, want, selected, yes)
}

// foundClient is an assistant detected on this computer, with what setup found
// in its configuration and what it ended up doing.
type foundClient struct {
	a       assistant
	i       inspection
	outcome string
	changed bool
}

func (c *foundClient) needsChange() bool { return c.i.state != identical && c.i.err == nil }

// configureClients detects the assistants in list and adds or updates the
// aulaglobal entry in each one the user agrees to. It reports whether any
// assistant failed.
func configureClients(u setupUI, e clientEnv, w desiredEntry, list []assistant, yes bool) bool {
	clients := listFoundClients(u, e, w, list)
	all := yes
	if !yes && countNeedingChange(clients) >= 2 {
		all = askConfigureAll(u, clients)
	}
	failed := false
	for _, c := range clients {
		fmt.Fprintln(u.out)
		if c.configure(u, e, w, all) {
			failed = true
		}
	}
	fmt.Fprintln(u.out, "\nResumen:")
	for _, c := range clients {
		fmt.Fprintln(u.out, "  "+c.a.Name()+": "+c.outcome)
	}
	changed := printReloadHints(u, clients)
	if changed {
		fmt.Fprintln(u.out, "\nPrueba a pedir: «¿Qué asignaturas tengo?»")
	} else if !failed {
		fmt.Fprintln(u.out, "\nNo se ha cambiado ninguna configuración.")
	}
	return failed
}

func listFoundClients(u setupUI, e clientEnv, w desiredEntry, list []assistant) []*foundClient {
	var clients []*foundClient
	var absent []string
	fmt.Fprintln(u.out, "Asistentes encontrados en este ordenador:")
	for _, a := range list {
		if !a.Detect(e) {
			absent = append(absent, a.Name())
			continue
		}
		clients = append(clients, &foundClient{a: a, i: a.Inspect(e, w)})
		fmt.Fprintln(u.out, "  ✓ "+a.Name())
	}
	if len(clients) == 0 {
		fmt.Fprintln(u.out, "  Ninguno.")
	}
	if len(absent) > 0 {
		fmt.Fprintln(u.out, "No encontrados: "+strings.Join(absent, ", "))
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
func askConfigureAll(u setupUI, clients []*foundClient) bool {
	fmt.Fprint(u.out, "\n› ¿Configurar todos? [S/n/e=elegir uno a uno] ")
	answer := strings.ToLower(strings.TrimSpace(u.line()))
	fmt.Fprintln(u.out)
	if answer == "n" {
		for _, c := range clients {
			if c.needsChange() {
				c.outcome = "omitido"
			}
		}
	}
	return answer == "" || acceptsYes(answer)
}

// configure explains what it found, asks (unless all is set) and applies the
// change. It reports whether the assistant could not be configured.
func (c *foundClient) configure(u setupUI, e clientEnv, w desiredEntry, all bool) (failed bool) {
	a, i := c.a, c.i
	fail := func() bool {
		c.outcome = "falló (ver instrucciones arriba)"
		fmt.Fprintln(u.out, "✗ "+a.Name()+": no se pudo configurar con seguridad.")
		fmt.Fprintln(u.out, a.Manual(w))
		return true
	}
	if i.err != nil {
		return fail()
	}
	if i.state == identical {
		c.outcome = "ya estaba"
		fmt.Fprintln(u.out, "✓ "+a.Name()+": ya está configurado.")
		return false
	}
	q := "¿Añadir AulaGlobal a " + a.Name() + "?"
	if i.state == different {
		fmt.Fprintln(u.out, "! "+a.Name()+": configuración diferente ("+i.detail+").")
		fmt.Fprintln(u.out, "  Se usará: "+w.Exe+" --transport stdio")
		q = "¿Actualizarlo?"
	} else if i.state == otherScope {
		fmt.Fprintln(u.out, "! "+a.Name()+": "+i.detail)
		if i.scope == "local" {
			q = "¿Mover AulaGlobal al ámbito de usuario?"
		} else {
			q = "¿Añadir también AulaGlobal al ámbito de usuario?"
		}
	}
	if c.outcome == "omitido" || !u.confirm(q, all) {
		c.outcome = "omitido"
		fmt.Fprintln(u.out, "· "+a.Name()+": omitido.")
		return false
	}
	if err := a.Apply(e, w, i.state != missing); err != nil {
		return fail()
	}
	c.changed = true
	c.outcome = "añadido"
	if i.state != missing {
		c.outcome = "actualizado"
	}
	fmt.Fprintln(u.out, "✓ "+a.Name()+": "+c.outcome+".")
	return false
}

// printReloadHints tells the user how to make each changed assistant pick up
// the new entry. It reports whether any assistant was changed.
func printReloadHints(u setupUI, clients []*foundClient) bool {
	changed := false
	for _, c := range clients {
		if c.changed {
			if !changed {
				fmt.Fprintln(u.out)
			}
			changed = true
			fmt.Fprintln(u.out, c.a.Name()+": "+c.a.Reload())
		}
	}
	return changed
}
