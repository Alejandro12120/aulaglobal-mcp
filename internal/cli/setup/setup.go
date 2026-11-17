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
	"aulaglobal-mcp/internal/clipboard"
	"aulaglobal-mcp/internal/termui"
	"aulaglobal-mcp/internal/userpaths"
	"golang.org/x/term"
)

// setup is the `aulaglobal-mcp setup` entry point. It runs four steps: choose
// the folders, get a token from the user's session cookie, check the token and
// connect the AI assistants.
func Run(args []string) (result error) {
	tokenFile, dataDir, err := userpaths.Defaults()
	if err != nil {
		return err
	}
	tokenFile = env.Or("AULAGLOBAL_TOKEN_FILE", tokenFile)
	dataDir = env.Or("AULAGLOBAL_DOWNLOAD_DIR", dataDir)
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	secretsDir := flags.String("secrets-dir", "", "Carpeta privada para el token (prioridad sobre la variable de entorno)")
	dataDirFlag := flags.String("data-dir", dataDir, "Carpeta de apuntes")
	clientIDs := flags.String("clients", "", "Asistentes separados por comas: "+assistants.IDs())
	yes := flags.Bool("yes", false, "Aceptar las opciones predeterminadas de los asistentes")
	onlyClients := flags.Bool("only-clients", false, "Configurar solo los asistentes, sin obtener ni comprobar el token")
	noPause := flags.Bool("no-pause", false, "Salir sin esperar Enter")
	if err = flags.Parse(args); err != nil {
		return err
	}
	selected, err := assistants.Select(*clientIDs)
	if err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("setup no acepta argumentos adicionales")
	}
	if *secretsDir != "" {
		tokenFile = filepath.Join(*secretsDir, "token")
	}
	tokenFile, err = filepath.Abs(tokenFile)
	if err != nil {
		return err
	}
	dataDir, err = filepath.Abs(*dataDirFlag)
	if err != nil {
		return err
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
			if info, err := os.Stat(tokenFile); err != nil || !info.Mode().IsRegular() {
				return errors.New("Primero ejecuta setup en una terminal para guardar el token, o usa --only-clients")
			}
			checkToken(context.Background(), aulaglobal.New(aulaglobal.Site, nil), tokenFile, ui)
		} else {
			if err := acquire(context.Background(), aulaglobal.New(aulaglobal.Site, nil), tokenFile, dataDir, ui, clipboard.New()); err != nil {
				return err
			}
		}
	}
	return connectThisComputer(ui, tokenFile, dataDir, selected, *yes)
}

// connectThisComputer is step 4 with the real machine: this executable, the
// user's home and configuration folders.
func connectThisComputer(ui termui.UI, tokenFile, dataDir string, selected []assistants.Assistant, yes bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
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
	defaultToken, defaultData, err := userpaths.Defaults()
	if err != nil {
		return err
	}
	want, err := assistants.NewEntry(exe, tokenFile, dataDir, defaultToken, defaultData)
	if err != nil {
		return err
	}
	if connectAssistants(ui, assistants.RealEnv(runtime.GOOS, home, config), want, selected, yes) {
		return errors.New("Algunos asistentes no se pudieron configurar; consulta las instrucciones anteriores")
	}
	return nil
}

// cookieCommand is shown at step 2. The DevTools console line that reads
// the MoodleSessionag cookie is based on AGDownloader (MIT, see
// THIRD_PARTY_NOTICES.md).
const cookieCommand = `copy(("; " + document.cookie).split("; MoodleSessionag=").pop().split(";").shift())`

// systemClipboard is the clipboard setup uses. Tests pass a fake.
type systemClipboard interface {
	Copy(string) error
	Read() (string, error)
	Clear() error
}

const cookieInstructions = `1. Entra en https://aulaglobal.uc3m.es e inicia sesión.

2. Pulsa F12 (Mac: Cmd + Option + I).
   Abre Consola.

3. %s

   ` + cookieCommand + `

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
func acquire(ctx context.Context, api *aulaglobal.Client, tokenFile, dataDir string, ui termui.UI, clipboard systemClipboard) error {
	if err := os.MkdirAll(filepath.Dir(tokenFile), 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	step(ui, 1, "Carpetas")
	fmt.Fprintln(ui.Out, "Tu token se guarda en:\n  "+tokenFile+"\n\nTus apuntes se guardarán en:\n  "+dataDir)
	step(ui, 2, "Tu cuenta de AulaGlobal")
	force := false
	if _, err := os.Lstat(tokenFile); err == nil {
		if keepExistingToken(ctx, api, tokenFile, ui) {
			return nil
		}
		force = true
	} else if !os.IsNotExist(err) {
		return err
	} else {
		fmt.Fprintln(ui.Out, "Se limpiarán los restos incompletos de un token anterior.")
		if err := cleanup(tokenFile); err != nil {
			return err
		}
	}
	commandInstruction := "Copia este comando, pégalo en la consola y pulsa Enter:"
	if clipboard.Copy(cookieCommand) == nil {
		commandInstruction = "✓ Comando copiado al portapapeles. Pégalo en la consola y pulsa Enter."
	}
	fmt.Fprintf(ui.Out, cookieInstructions+"\n", commandInstruction)
	cookie, err := askCookie(ui)
	if err != nil {
		return err
	}
	tokenValue, privateToken, err := api.TokenFromCookie(ctx, cookie)
	if errors.Is(err, aulaglobal.ErrRateLimited) {
		return err
	}
	if err != nil {
		return errors.New("No se pudo obtener el token. Cierra sesión, vuelve a entrar en AulaGlobal y repite setup.")
	}
	if contents, err := clipboard.Read(); err == nil && contents == cookie {
		if clipboard.Clear() == nil {
			fmt.Fprintln(ui.Out, "· Portapapeles vaciado.")
		}
	}
	if err = aulaglobal.SaveSecret(tokenFile, tokenValue, force); err != nil {
		return err
	}
	if privateToken != "" {
		if err = aulaglobal.SaveSecret(tokenFile+".private", privateToken, force); err != nil {
			return err
		}
	} else if force {
		if err = os.Remove(tokenFile + ".private"); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err = saveTokenMeta(tokenFile, force); err != nil {
		return err
	}
	checkToken(ctx, api, tokenFile, ui)
	return nil
}

// keepExistingToken tells the user about the token already saved, checks it and
// reports whether the user keeps it. An expired token is never kept: it needs
// no question, only a new cookie.
func keepExistingToken(ctx context.Context, api *aulaglobal.Client, tokenFile string, ui termui.UI) bool {
	fmt.Fprintln(ui.Out, "Ya tienes un token guardado.")
	var meta struct {
		ObtainedAt string `json:"obtained_at"`
	}
	savedMeta, _ := os.ReadFile(tokenFile + ".meta.json")
	if json.Unmarshal(savedMeta, &meta) == nil && meta.ObtainedAt != "" {
		fmt.Fprintln(ui.Out, "Obtenido el:", meta.ObtainedAt)
	}
	api.Tokens = aulaglobal.StaticToken{File: tokenFile}
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
		typed, err := ui.Password()
		fmt.Fprintln(ui.Out)
		if err != nil {
			return "", errors.New("No se pudo leer. Abre una terminal y ejecuta el programa con setup.")
		}
		cookie = normalizeCookie(string(typed))
		clear(typed)
		if cookie != "" {
			return cookie, nil
		}
		fmt.Fprintln(ui.Out, "! No has pegado ningún valor.\n  Copia el valor de MoodleSessionag e inténtalo otra vez.")
	}
	return "", errors.New("No se recibió ninguna cookie")
}

// normalizeCookie strips the whitespace, quotes and "MoodleSessionag=" prefix
// that people copy along with the value.
func normalizeCookie(raw string) string {
	for i := 0; i < 3; i++ {
		raw = strings.TrimSpace(raw)
		if len(raw) >= 2 && ((raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'')) {
			raw = raw[1 : len(raw)-1]
		}
		raw = strings.TrimPrefix(raw, "MoodleSessionag=")
	}
	return strings.TrimSpace(raw)
}

// checkToken is step 3: ask Moodle whether the saved token works.
func checkToken(ctx context.Context, api *aulaglobal.Client, tokenFile string, ui termui.UI) {
	api.Tokens = aulaglobal.StaticToken{File: tokenFile}
	auth, err := api.Auth(ctx)
	reportToken(ui, auth, err)
}

func reportToken(ui termui.UI, auth aulaglobal.Auth, err error) {
	step(ui, 3, "Comprobación")
	switch {
	case errors.Is(err, aulaglobal.ErrTokenExpired):
		fmt.Fprintln(ui.Out, "! El token ha caducado o ya no es válido.\n  Repite setup para renovarlo.")
	case err != nil:
		fmt.Fprintln(ui.Out, "! No se pudo confirmar el token.\n  Comprueba auth_status o repite setup.")
	default:
		fmt.Fprintln(ui.Out, "✓ Token válido. Hola, "+auth.Fullname)
	}
}

// saveTokenMeta records when the token was obtained, next to the token file.
func saveTokenMeta(tokenFile string, force bool) error {
	meta, _ := json.Marshal(map[string]string{"obtained_at": time.Now().UTC().Format(time.RFC3339)})
	return aulaglobal.SaveSecret(tokenFile+".meta.json", string(meta), force)
}

// cleanup deletes what an interrupted setup left next to a missing token.
func cleanup(tokenFile string) error {
	paths := []string{tokenFile + ".meta.json", tokenFile + ".private"}
	temps, err := filepath.Glob(filepath.Join(filepath.Dir(tokenFile), ".token-*"))
	if err != nil {
		return err
	}
	paths = append(paths, temps...)
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// step prints the header of one of the four setup steps, in bold on a terminal.
func step(ui termui.UI, number int, title string) {
	header := fmt.Sprintf("── Paso %d de 4 · %s ──", number, title)
	if info, ok := ui.Out.(*os.File); ok && runtime.GOOS != "windows" && term.IsTerminal(int(info.Fd())) {
		if _, disabled := os.LookupEnv("NO_COLOR"); !disabled {
			header = "\033[1m" + header + "\033[0m"
		}
	}
	fmt.Fprintf(ui.Out, "\n%s\n\n", header)
}
