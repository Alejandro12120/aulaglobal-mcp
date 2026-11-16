package setup

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"aulaglobal-mcp/internal/assistants"
	"aulaglobal-mcp/internal/aulaglobal"
	"aulaglobal-mcp/internal/cli/env"
	"aulaglobal-mcp/internal/termui"
	"aulaglobal-mcp/internal/userpaths"
	"golang.org/x/term"
)

// setup is the `aulaglobal-mcp setup` entry point. It runs four steps: choose
// the folders, get a token from the user's session cookie, check the token and
// connect the AI assistants.
func Run(args []string) (result error) {
	token, data, e := userpaths.Defaults()
	if e != nil {
		return e
	}
	token = env.Or("AULAGLOBAL_TOKEN_FILE", token)
	data = env.Or("AULAGLOBAL_DOWNLOAD_DIR", data)
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	secrets := flags.String("secrets-dir", "", "Carpeta privada para el token (prioridad sobre la variable de entorno)")
	downloads := flags.String("data-dir", data, "Carpeta de apuntes")
	clients := flags.String("clients", "", "Asistentes separados por comas: "+assistants.IDs())
	yes := flags.Bool("yes", false, "Aceptar las opciones predeterminadas de los asistentes")
	onlyClients := flags.Bool("only-clients", false, "Configurar solo los asistentes, sin obtener ni comprobar el token")
	noPause := flags.Bool("no-pause", false, "Salir sin esperar Enter")
	if e = flags.Parse(args); e != nil {
		return e
	}
	selected, err := assistants.Select(*clients)
	if err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("setup no acepta argumentos adicionales")
	}
	if *secrets != "" {
		token = filepath.Join(*secrets, "token")
	}
	token, e = filepath.Abs(token)
	if e != nil {
		return e
	}
	data, e = filepath.Abs(*downloads)
	if e != nil {
		return e
	}
	ui := termui.UI{In: os.Stdin, Out: os.Stdout, Password: func() ([]byte, error) { return term.ReadPassword(int(os.Stdin.Fd())) }}
	if !term.IsTerminal(int(os.Stdin.Fd())) && !*yes {
		return errors.New("Abre una terminal y ejecuta aulaglobal-mcp setup")
	}
	if !*noPause && !*yes {
		defer func() {
			if result != nil {
				fmt.Fprintln(ui.Out, "\n✗ "+result.Error())
			}
			fmt.Fprint(ui.Out, "\n› Pulsa Enter para salir ")
			ui.Line()
		}()
	}
	if !*onlyClients {
		if *yes {
			if f, err := os.Stat(token); err != nil || !f.Mode().IsRegular() {
				return errors.New("Primero ejecuta setup en una terminal para guardar el token, o usa --only-clients")
			}
			checkToken(context.Background(), aulaglobal.New(aulaglobal.Site, nil), token, ui)
		} else {
			if e := acquire(context.Background(), aulaglobal.New(aulaglobal.Site, nil), token, data, ui); e != nil {
				return e
			}
		}
	}
	return connectThisComputer(ui, token, data, selected, *yes)
}

// connectThisComputer is step 4 with the real machine: this executable, the
// user's home and configuration folders.
func connectThisComputer(ui termui.UI, token, data string, selected []assistants.Assistant, yes bool) error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	exe, e = filepath.EvalSymlinks(exe)
	if e != nil {
		return e
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	config, e := os.UserConfigDir()
	if e != nil {
		return e
	}
	defaultToken, defaultData, e := userpaths.Defaults()
	if e != nil {
		return e
	}
	want, e := assistants.NewEntry(exe, token, data, defaultToken, defaultData)
	if e != nil {
		return e
	}
	if connectAssistants(ui, assistants.RealEnv(runtime.GOOS, home, config), want, selected, yes) {
		return errors.New("Algunos asistentes no se pudieron configurar; consulta las instrucciones anteriores")
	}
	return nil
}

// cookieInstructions is shown at step 2. The DevTools console line that reads
// the MoodleSessionag cookie is based on AGDownloader (MIT, see
// THIRD_PARTY_NOTICES.md).
const cookieInstructions = `1. Entra en https://aulaglobal.uc3m.es e inicia sesión.

2. Pulsa F12 (Mac: Cmd + Option + I).
   Abre Consola.

3. Pega este comando y pulsa Enter:

   copy(("; " + document.cookie).split("; MoodleSessionag=").pop().split(";").shift())

   Si Chrome bloquea pegar, escribe «allow pasting» primero.
   También puede aparecer como «permitir pegar».

   Otra opción: Aplicación → Cookies → https://aulaglobal.uc3m.es
   Copia el Valor de MoodleSessionag.
   En Firefox: Almacenamiento → Cookies.

4. Vuelve aquí y pega la cookie.
   No se verá mientras escribes.

! No compartas la cookie ni el token.`

// acquire runs steps 1 to 3: it shows the folders, gets a token from the
// user's session cookie, saves it and checks it. If the user keeps the token
// already saved, it only checks that one.
func acquire(ctx context.Context, api *aulaglobal.Client, token, data string, ui termui.UI) error {
	if e := os.MkdirAll(filepath.Dir(token), 0700); e != nil {
		return e
	}
	if e := os.MkdirAll(data, 0700); e != nil {
		return e
	}
	step(ui, 1, "Carpetas")
	fmt.Fprintln(ui.Out, "Tu token se guarda en:\n  "+token+"\n\nTus apuntes se guardarán en:\n  "+data)
	step(ui, 2, "Tu cuenta de AulaGlobal")
	force := false
	if _, e := os.Lstat(token); e == nil {
		if keepExistingToken(ctx, api, token, ui) {
			return nil
		}
		force = true
	} else if !os.IsNotExist(e) {
		return e
	} else {
		fmt.Fprintln(ui.Out, "Se limpiarán los restos incompletos de un token anterior.")
		if e := cleanup(token); e != nil {
			return e
		}
	}
	fmt.Fprintln(ui.Out, cookieInstructions)
	cookie, e := askCookie(ui)
	if e != nil {
		return e
	}
	tokenValue, private, e := api.TokenFromCookie(ctx, cookie)
	if errors.Is(e, aulaglobal.ErrRateLimited) {
		return e
	}
	if e != nil {
		return errors.New("No se pudo obtener el token. Cierra sesión, vuelve a entrar en AulaGlobal y repite setup.")
	}
	if e = aulaglobal.SaveSecret(token, tokenValue, force); e != nil {
		return e
	}
	if private != "" {
		if e = aulaglobal.SaveSecret(token+".private", private, force); e != nil {
			return e
		}
	} else if force {
		if e = os.Remove(token + ".private"); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	if e = saveTokenMeta(token, force); e != nil {
		return e
	}
	checkToken(ctx, api, token, ui)
	return nil
}

// keepExistingToken tells the user about the token already saved, checks it and
// reports whether the user keeps it. An expired token is never kept: it needs
// no question, only a new cookie.
func keepExistingToken(ctx context.Context, api *aulaglobal.Client, token string, ui termui.UI) bool {
	fmt.Fprintln(ui.Out, "Ya tienes un token guardado.")
	var meta struct {
		ObtainedAt string `json:"obtained_at"`
	}
	b, _ := os.ReadFile(token + ".meta.json")
	if json.Unmarshal(b, &meta) == nil && meta.ObtainedAt != "" {
		fmt.Fprintln(ui.Out, "Obtenido el:", meta.ObtainedAt)
	}
	api.Tokens = aulaglobal.StaticToken{File: token}
	auth, err := api.Auth(ctx)
	if errors.Is(err, aulaglobal.ErrTokenExpired) {
		fmt.Fprintln(ui.Out, "! Tu token ha caducado o ya no es válido. Vamos a renovarlo.")
		fmt.Fprintln(ui.Out)
		return false
	}
	if err != nil {
		fmt.Fprintln(ui.Out, "! No se ha podido comprobar si sigue siendo válido (¿sin conexión?).")
	}
	if ui.Yes("¿Quieres reemplazarlo? [s/N]") {
		return false
	}
	if err == nil {
		fmt.Fprintln(ui.Out, "✓ Conservamos tu token.")
	} else {
		fmt.Fprintln(ui.Out, "Conservamos tu token.")
	}
	reportToken(ui, auth, err)
	return true
}

// askCookie reads the cookie without echo, with three attempts for an empty answer.
func askCookie(ui termui.UI) (string, error) {
	var cookie string
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(ui.Out, "\n› Pega la cookie y pulsa Enter (intento %d/3): ", i)
		b, e := ui.Password()
		fmt.Fprintln(ui.Out)
		if e != nil {
			return "", errors.New("No se pudo leer. Abre una terminal y ejecuta el programa con setup.")
		}
		cookie = normalizeCookie(string(b))
		clear(b)
		if cookie != "" {
			return cookie, nil
		}
		fmt.Fprintln(ui.Out, "! No has pegado ningún valor.\n  Copia el valor de MoodleSessionag e inténtalo otra vez.")
	}
	return "", errors.New("No se recibió ninguna cookie")
}

// normalizeCookie strips the whitespace, quotes and "MoodleSessionag=" prefix
// that people copy along with the value.
func normalizeCookie(s string) string {
	for i := 0; i < 3; i++ {
		s = strings.TrimSpace(s)
		if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
			s = s[1 : len(s)-1]
		}
		s = strings.TrimPrefix(s, "MoodleSessionag=")
	}
	return strings.TrimSpace(s)
}

// checkToken is step 3: ask Moodle whether the saved token works.
func checkToken(ctx context.Context, api *aulaglobal.Client, token string, ui termui.UI) {
	api.Tokens = aulaglobal.StaticToken{File: token}
	auth, e := api.Auth(ctx)
	reportToken(ui, auth, e)
}

func reportToken(ui termui.UI, auth aulaglobal.Auth, e error) {
	step(ui, 3, "Comprobación")
	switch {
	case errors.Is(e, aulaglobal.ErrTokenExpired):
		fmt.Fprintln(ui.Out, "! El token ha caducado o ya no es válido.\n  Repite setup para renovarlo.")
	case e != nil:
		fmt.Fprintln(ui.Out, "! No se pudo confirmar el token.\n  Comprueba auth_status o repite setup.")
	default:
		fmt.Fprintln(ui.Out, "✓ Token válido. Hola, "+auth.Fullname)
	}
}

// saveTokenMeta records when the token was obtained, next to the token file.
func saveTokenMeta(token string, force bool) error {
	meta, _ := json.Marshal(map[string]string{"obtained_at": time.Now().UTC().Format(time.RFC3339)})
	return aulaglobal.SaveSecret(token+".meta.json", string(meta), force)
}

// cleanup deletes what an interrupted setup left next to a missing token.
func cleanup(token string) error {
	paths := []string{token + ".meta.json", token + ".private"}
	temps, e := filepath.Glob(filepath.Join(filepath.Dir(token), ".token-*"))
	if e != nil {
		return e
	}
	paths = append(paths, temps...)
	for _, p := range paths {
		if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	return nil
}

// step prints the header of one of the four setup steps, in bold on a terminal.
func step(ui termui.UI, n int, title string) {
	header := fmt.Sprintf("── Paso %d de 4 · %s ──", n, title)
	if f, ok := ui.Out.(*os.File); ok && runtime.GOOS != "windows" && term.IsTerminal(int(f.Fd())) {
		if _, disabled := os.LookupEnv("NO_COLOR"); !disabled {
			header = "\033[1m" + header + "\033[0m"
		}
	}
	fmt.Fprintf(ui.Out, "\n%s\n\n", header)
}
