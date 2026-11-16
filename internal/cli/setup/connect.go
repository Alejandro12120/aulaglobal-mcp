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
func connectAssistants(ui termui.UI, env assistants.Env, want assistants.Entry, selected []assistants.Assistant, yes bool) bool {
	step(ui, 4, "Conectar con tus asistentes")
	fmt.Fprint(ui.Out, privacyNotice+"\n")
	return configureClients(ui, env, want, selected, yes)
}

// foundClient is an assistant detected on this computer, with what setup found
// in its configuration and what it ended up doing.
type foundClient struct {
	assistant  assistants.Assistant
	inspection assistants.Inspection
	outcome    string
	changed    bool
}

func (client *foundClient) needsChange() bool {
	return client.inspection.State != assistants.Identical && client.inspection.Err == nil
}

// configureClients detects the assistants in candidates and adds or updates the
// aulaglobal entry in each one the user agrees to. It reports whether any
// assistant failed.
func configureClients(ui termui.UI, env assistants.Env, want assistants.Entry, candidates []assistants.Assistant, yes bool) bool {
	clients := listFoundClients(ui, env, want, candidates)
	all := yes
	if !yes && countNeedingChange(clients) >= 2 {
		all = askConfigureAll(ui, clients)
	}
	failed := false
	for _, client := range clients {
		fmt.Fprintln(ui.Out)
		if client.configure(ui, env, want, all) {
			failed = true
		}
	}
	fmt.Fprintln(ui.Out, "\nResumen:")
	for _, client := range clients {
		fmt.Fprintln(ui.Out, "  "+client.assistant.Name()+": "+client.outcome)
	}
	changed := printReloadHints(ui, clients)
	if changed {
		fmt.Fprintln(ui.Out, "\nPrueba a pedir: «¿Qué asignaturas tengo?»")
	} else if !failed {
		fmt.Fprintln(ui.Out, "\nNo se ha cambiado ninguna configuración.")
	}
	return failed
}

func listFoundClients(ui termui.UI, env assistants.Env, want assistants.Entry, candidates []assistants.Assistant) []*foundClient {
	var clients []*foundClient
	var absent []string
	fmt.Fprintln(ui.Out, "Asistentes encontrados en este ordenador:")
	for _, assistant := range candidates {
		if !assistant.Detect(env) {
			absent = append(absent, assistant.Name())
			continue
		}
		clients = append(clients, &foundClient{assistant: assistant, inspection: assistant.Inspect(env, want)})
		fmt.Fprintln(ui.Out, "  ✓ "+assistant.Name())
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
	count := 0
	for _, client := range clients {
		if client.needsChange() {
			count++
		}
	}
	return count
}

// askConfigureAll asks once for all assistants. Answering "n" skips every
// assistant that needs a change; anything else that is not yes asks one by one.
func askConfigureAll(ui termui.UI, clients []*foundClient) bool {
	fmt.Fprint(ui.Out, "\n› ¿Configurar todos? [S/n/e=elegir uno a uno] ")
	answer := strings.ToLower(strings.TrimSpace(ui.Line()))
	fmt.Fprintln(ui.Out)
	if answer == "n" {
		for _, client := range clients {
			if client.needsChange() {
				client.outcome = "omitido"
			}
		}
	}
	return answer == "" || termui.AcceptsYes(answer)
}

// configure explains what it found, asks (unless all is set) and applies the
// change. It reports whether the assistant could not be configured.
func (client *foundClient) configure(ui termui.UI, env assistants.Env, want assistants.Entry, all bool) (failed bool) {
	assistant, inspection := client.assistant, client.inspection
	fail := func() bool {
		client.outcome = "falló (ver instrucciones arriba)"
		fmt.Fprintln(ui.Out, "✗ "+assistant.Name()+": no se pudo configurar con seguridad.")
		fmt.Fprintln(ui.Out, assistant.Manual(want))
		return true
	}
	if inspection.Err != nil {
		return fail()
	}
	if inspection.State == assistants.Identical {
		client.outcome = "ya estaba"
		fmt.Fprintln(ui.Out, "✓ "+assistant.Name()+": ya está configurado.")
		return false
	}
	question := "¿Añadir AulaGlobal a " + assistant.Name() + "?"
	if inspection.State == assistants.Different {
		fmt.Fprintln(ui.Out, "! "+assistant.Name()+": configuración diferente ("+inspection.Detail+").")
		fmt.Fprintln(ui.Out, "  Se usará: "+want.Exe+" --transport stdio")
		question = "¿Actualizarlo?"
	} else if inspection.State == assistants.OtherScope {
		fmt.Fprintln(ui.Out, "! "+assistant.Name()+": "+inspection.Detail)
		if inspection.Scope == "local" {
			question = "¿Mover AulaGlobal al ámbito de usuario?"
		} else {
			question = "¿Añadir también AulaGlobal al ámbito de usuario?"
		}
	}
	if client.outcome == "omitido" || !ui.Confirm(question, all) {
		client.outcome = "omitido"
		fmt.Fprintln(ui.Out, "· "+assistant.Name()+": omitido.")
		return false
	}
	if err := assistant.Apply(env, want, inspection.State != assistants.Missing); err != nil {
		return fail()
	}
	client.changed = true
	client.outcome = "añadido"
	if inspection.State != assistants.Missing {
		client.outcome = "actualizado"
	}
	fmt.Fprintln(ui.Out, "✓ "+assistant.Name()+": "+client.outcome+".")
	return false
}

// printReloadHints tells the user how to make each changed assistant pick up
// the new entry. It reports whether any assistant was changed.
func printReloadHints(ui termui.UI, clients []*foundClient) bool {
	changed := false
	for _, client := range clients {
		if client.changed {
			if !changed {
				fmt.Fprintln(ui.Out)
			}
			changed = true
			fmt.Fprintln(ui.Out, client.assistant.Name()+": "+client.assistant.Reload())
		}
	}
	return changed
}
