package main

import (
	"aulaglobal-mcp/internal/aulaglobal"
	"aulaglobal-mcp/internal/userpaths"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/term"
)

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

type setupUI struct {
	in       io.Reader
	out      io.Writer
	password func() ([]byte, error)
}

func (u setupUI) line() string {
	// Read one byte at a time: a buffered reader must not consume the hidden input.
	var b [1]byte
	var s strings.Builder
	for {
		n, e := u.in.Read(b[:])
		if n > 0 {
			if b[0] == '\n' {
				break
			}
			if b[0] != '\r' {
				s.WriteByte(b[0])
			}
		}
		if e != nil {
			break
		}
	}
	return s.String()
}
func (u setupUI) yes(q string) bool {
	fmt.Fprint(u.out, "\n› "+q+" ")
	s := strings.ToLower(strings.TrimSpace(u.line()))
	return s == "s" || s == "si" || s == "sí"
}
func (u setupUI) step(n int, title string) {
	header := fmt.Sprintf("── Paso %d de 4 · %s ──", n, title)
	if f, ok := u.out.(*os.File); ok && runtime.GOOS != "windows" && term.IsTerminal(int(f.Fd())) {
		if _, disabled := os.LookupEnv("NO_COLOR"); !disabled {
			header = "\033[1m" + header + "\033[0m"
		}
	}
	fmt.Fprintf(u.out, "\n%s\n\n", header)
}
func checkToken(ctx context.Context, api *aulaglobal.Client, token string, u setupUI) {
	u.step(3, "Comprobación")
	api.Tokens = aulaglobal.StaticToken{File: token}
	auth, e := api.Auth(ctx)
	if e != nil {
		fmt.Fprintln(u.out, "! No se pudo confirmar el token.\n  Comprueba auth_status o repite setup.")
	} else {
		fmt.Fprintln(u.out, "✓ Token válido. Hola, "+auth.Fullname)
	}
}

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
func acquire(ctx context.Context, api *aulaglobal.Client, token, data string, u setupUI) (bool, error) {
	if e := os.MkdirAll(filepath.Dir(token), 0700); e != nil {
		return false, e
	}
	if e := os.MkdirAll(data, 0700); e != nil {
		return false, e
	}
	u.step(1, "Carpetas")
	fmt.Fprintln(u.out, "Tu token se guarda en:\n  "+token+"\n\nTus apuntes se guardarán en:\n  "+data)
	u.step(2, "Tu cuenta de AulaGlobal")
	force := false
	if _, e := os.Lstat(token); e == nil {
		fmt.Fprintln(u.out, "Ya tienes un token guardado.")
		var meta struct {
			ObtainedAt string `json:"obtained_at"`
		}
		b, _ := os.ReadFile(token + ".meta.json")
		if json.Unmarshal(b, &meta) == nil && meta.ObtainedAt != "" {
			fmt.Fprintln(u.out, "Obtenido el:", meta.ObtainedAt)
		}
		if !u.yes("¿Quieres reemplazarlo? [s/N]") {
			fmt.Fprintln(u.out, "✓ Conservamos tu token.")
			checkToken(ctx, api, token, u)
			return true, nil
		}
		force = true
	} else if !os.IsNotExist(e) {
		return false, e
	} else {
		fmt.Fprintln(u.out, "Se limpiarán los restos incompletos de un token anterior.")
		if e := cleanup(token); e != nil {
			return false, e
		}
	}
	fmt.Fprintln(u.out, `1. Entra en https://aulaglobal.uc3m.es e inicia sesión.

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

! No compartas la cookie ni el token.`)
	var cookie string
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(u.out, "\n› Pega la cookie y pulsa Enter (intento %d/3): ", i)
		b, e := u.password()
		fmt.Fprintln(u.out)
		if e != nil {
			return false, errors.New("No se pudo leer. Abre una terminal y ejecuta el programa con setup.")
		}
		cookie = normalizeCookie(string(b))
		clear(b)
		if cookie != "" {
			break
		}
		fmt.Fprintln(u.out, "! No has pegado ningún valor.\n  Copia el valor de MoodleSessionag e inténtalo otra vez.")
	}
	if cookie == "" {
		return false, errors.New("No se recibió ninguna cookie")
	}
	tokenValue, private, e := api.TokenFromCookie(ctx, cookie)
	cookie = ""
	if e != nil {
		return false, errors.New("No se pudo obtener el token. Cierra sesión, vuelve a entrar en AulaGlobal y repite setup.")
	}
	if e = aulaglobal.SaveSecret(token, tokenValue, force); e != nil {
		return false, e
	}
	if private != "" {
		if e = aulaglobal.SaveSecret(token+".private", private, force); e != nil {
			return false, e
		}
	} else if force {
		if e = os.Remove(token + ".private"); e != nil && !os.IsNotExist(e) {
			return false, e
		}
	}
	meta, _ := json.Marshal(map[string]string{"obtained_at": time.Now().UTC().Format(time.RFC3339)})
	if e = aulaglobal.SaveSecret(token+".meta.json", string(meta), force); e != nil {
		return false, e
	}
	checkToken(ctx, api, token, u)
	return true, nil
}

func atomicConfig(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	old, e := os.ReadFile(path)
	if e == nil {
		if e = writeAtomic(path+".bak", old); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	return writeAtomic(path, b)
}
func writeAtomic(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".config-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func claudeConfig(old []byte, entry map[string]any) ([]byte, error) {
	root := map[string]json.RawMessage{}
	if len(old) > 0 {
		if e := json.Unmarshal(old, &root); e != nil {
			return nil, e
		}
		if root == nil {
			return nil, errors.New("Configuración JSON vacía")
		}
	}
	servers := map[string]json.RawMessage{}
	if b, ok := root["mcpServers"]; ok {
		if e := json.Unmarshal(b, &servers); e != nil {
			return nil, e
		}
		if servers == nil {
			return nil, errors.New("mcpServers debe ser un objeto")
		}
	}
	b, e := json.Marshal(entry)
	if e != nil {
		return nil, e
	}
	servers["aulaglobal"] = b
	root["mcpServers"], e = json.Marshal(servers)
	if e != nil {
		return nil, e
	}
	return json.MarshalIndent(root, "", "  ")
}

// TOML is validated with a small parser. Appending preserves comments and unrelated
// bytes; replacement is deliberately refused for multiline strings (ambiguous
// line-based table boundaries). The user can edit that unusual case manually.
func codexConfig(old []byte, entry map[string]any, replace bool) ([]byte, bool, error) {
	var root map[string]any
	if e := toml.Unmarshal(old, &root); e != nil {
		return nil, false, e
	}
	exists := false
	if servers, ok := root["mcp_servers"].(map[string]any); ok {
		_, exists = servers["aulaglobal"]
	}
	if exists && !replace {
		return nil, true, nil
	}
	block, e := toml.Marshal(map[string]any{"mcp_servers": map[string]any{"aulaglobal": entry}})
	if e != nil {
		return nil, exists, e
	}
	if !exists {
		out := append(append([]byte{}, old...), '\n')
		out = append(out, block...)
		var check map[string]any
		e = toml.Unmarshal(out, &check)
		return out, false, e
	}
	if strings.Contains(string(old), "\"\"\"") || strings.Contains(string(old), "'''") {
		return nil, true, errors.New("El TOML contiene texto multilínea. Añade aulaglobal manualmente; no se ha cambiado nada.")
	}
	var out strings.Builder
	skipping := false
	found := false
	scanner := bufio.NewScanner(strings.NewReader(string(old)))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			var header map[string]any
			if e := toml.Unmarshal([]byte(line+"\n"), &header); e != nil {
				return nil, true, e
			}
			target := false
			if servers, ok := header["mcp_servers"].(map[string]any); ok {
				_, target = servers["aulaglobal"]
			}
			skipping = target
			if target {
				found = true
			}
		}
		if !skipping {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	if e = scanner.Err(); e != nil {
		return nil, true, e
	}
	if !found {
		return nil, true, errors.New("Formato TOML no sustituible con seguridad; edítalo manualmente")
	}
	out.Write(block)
	var check map[string]any
	e = toml.Unmarshal([]byte(out.String()), &check)
	return []byte(out.String()), true, e
}
func register(u setupUI, exe, token, data, claude, codex string) error {
	entry := map[string]any{"command": exe, "args": []string{"--transport", "stdio"}, "env": map[string]string{"AULAGLOBAL_TOKEN_FILE": token, "AULAGLOBAL_DOWNLOAD_DIR": data}}
	for _, c := range []struct {
		path, name string
		json       bool
	}{{claude, "Claude Desktop", true}, {codex, "Codex / ChatGPT desktop", false}} {
		if _, e := os.Stat(filepath.Dir(c.path)); e != nil {
			fmt.Fprintln(u.out, "! "+c.name+": no detectado.")
			continue
		}
		fmt.Fprintln(u.out, "\n✓ "+c.name+": detectado.")
		if !u.yes("¿Añadir AulaGlobal a " + c.name + "? [s/N]") {
			continue
		}
		old, e := os.ReadFile(c.path)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		var b []byte
		if c.json {
			var root map[string]json.RawMessage
			if len(old) > 0 && json.Unmarshal(old, &root) != nil {
				return errors.New("JSON inválido; no se ha cambiado nada")
			}
			var servers map[string]json.RawMessage
			_ = json.Unmarshal(root["mcpServers"], &servers)
			if _, ok := servers["aulaglobal"]; ok && !u.yes("Ya existe aulaglobal. ¿Quieres reemplazarlo? [s/N]") {
				continue
			}
			b, e = claudeConfig(old, entry)
		} else {
			var exists bool
			b, exists, e = codexConfig(old, entry, false)
			if e == nil && exists {
				if !u.yes("Ya existe aulaglobal. ¿Quieres reemplazarlo? [s/N]") {
					continue
				}
				b, _, e = codexConfig(old, entry, true)
			}
		}
		if e != nil {
			return e
		}
		if e = atomicConfig(c.path, b); e != nil {
			return e
		}
		fmt.Fprintln(u.out, "✓ Configuración guardada en:\n ", c.path, "\n  Copia anterior (si existía): .bak.")
	}
	return nil
}
func setup(args []string) (result error) {
	token, data, e := userpaths.Defaults()
	if e != nil {
		return e
	}
	token = env("AULAGLOBAL_TOKEN_FILE", token)
	data = env("AULAGLOBAL_DOWNLOAD_DIR", data)
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	secrets := flags.String("secrets-dir", "", "Carpeta privada para el token (prioridad sobre la variable de entorno)")
	downloads := flags.String("data-dir", data, "Carpeta de apuntes")
	noPause := flags.Bool("no-pause", false, "Salir sin esperar Enter")
	if e = flags.Parse(args); e != nil {
		return e
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
	u := setupUI{os.Stdin, os.Stdout, func() ([]byte, error) { return term.ReadPassword(int(os.Stdin.Fd())) }}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("Abre una terminal y ejecuta aulaglobal-mcp setup")
	}
	if !*noPause {
		defer func() {
			if result != nil {
				fmt.Fprintln(u.out, "\n✗ "+result.Error())
			}
			fmt.Fprint(u.out, "\n› Pulsa Enter para salir ")
			u.line()
		}()
	}
	ok, e := acquire(context.Background(), aulaglobal.New(aulaglobal.Site, nil), token, data, u)
	if e != nil {
		return e
	}
	if !ok {
		return nil
	}
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
	u.step(4, "Conectar con tus asistentes")
	cli := findClaude(runtime.GOOS, home, os.Getenv("APPDATA"), exec.LookPath, os.Stat)
	dt, dd, e := userpaths.Defaults()
	if e != nil {
		return e
	}
	extra := []string{}
	if token != dt {
		extra = append(extra, "-e", "AULAGLOBAL_TOKEN_FILE="+token)
	}
	if data != dd {
		extra = append(extra, "-e", "AULAGLOBAL_DOWNLOAD_DIR="+data)
	}
	openDir := userpaths.OpenCode(home, os.Getenv("XDG_CONFIG_HOME"))
	registerOpenCode(u, findOpenCode(runtime.GOOS, home, openDir, exec.LookPath, os.Stat), openDir, exe, extra)
	fallback := registerCode(u, cli, exe, extra, runClaude)
	if e = register(u, exe, token, data, userpaths.Claude(runtime.GOOS, home, config), filepath.Join(home, ".codex", "config.toml")); e != nil {
		return e
	}
	fmt.Fprintln(u.out, "\n✓ Todo listo.\n\nReinicia tu asistente o abre una sesión nueva.\nEn Claude Code, usa /mcp para comprobar la conexión.\nPrueba a pedir: «¿Qué asignaturas tengo?»")
	if fallback {
		manualCode(u, cli, exe, extra)
	}
	return nil
}
