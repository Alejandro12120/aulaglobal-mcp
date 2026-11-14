# AulaGlobal MCP

Conecta tu asistente de IA (Claude Code, Claude Desktop u otro cliente MCP) con
[AulaGlobal](https://aulaglobal.uc3m.es) de la UC3M. Con él, el asistente puede:

- ver tus asignaturas,
- descargar los apuntes y materiales a tu ordenador,
- leerlos (PDF, Word, PowerPoint, texto…) y buscar dentro de ellos,
- consultar tus entregas pendientes y su estado.

Ejemplos de lo que puedes pedirle:

> «¿Qué entregas tengo esta semana?»
> «Descarga los PDF de Sistemas Operativos y resúmeme el tema 3.»
> «Busca en mis apuntes dónde se explica el algoritmo de Dijkstra.»

Todo se guarda en tu ordenador. El asistente nunca ve tu contraseña ni tu token.

---

## Lo que necesitas

- **Linux o macOS** con una terminal (en Windows, usa WSL).
- **[Docker](https://docs.docker.com/get-docker/)** instalado. Solo se usa para
  compilar el programa; no hace falta instalar Go.
- Un cliente MCP, por ejemplo **[Claude Code](https://claude.com/claude-code)**.

---

## Paso 1 · Descargar y compilar

Abre una terminal y ejecuta:

```bash
git clone <url-de-este-repositorio> aulaglobal-mcp
cd aulaglobal-mcp

docker run --rm -u "$(id -u):$(id -g)" -e GOCACHE=/tmp/gocache -e GOPATH=/tmp/gopath \
  -v "$PWD":/src -w /src golang:1.26.9 \
  sh -c 'CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o aulaglobal-mcp ./cmd/aulaglobal-mcp'

mkdir -p secrets data && chmod 700 secrets data
```

Al terminar tendrás un archivo llamado `aulaglobal-mcp` en la carpeta. Ese es el
programa.

---

## Paso 2 · Conseguir tu token de AulaGlobal

El programa necesita un *token*: una clave que permite leer AulaGlobal en tu
nombre, la misma que usa la app móvil de Moodle. Se obtiene a partir de la
cookie de sesión del navegador.

Ejecuta `./obtener-token.sh` desde esta carpeta. Te guiará para copiar la cookie,
la leerá sin mostrarla y guardará el token con permisos privados. Pregunta antes
de reemplazarlo y comprueba la conexión si tienes curl.

### 2.1 Copiar la cookie desde el navegador

1. Entra en <https://aulaglobal.uc3m.es> e **inicia sesión** como siempre.
2. Pulsa **F12** (en Mac: `Cmd + Option + I`) para abrir las herramientas de
   desarrollador.
3. Ve a la pestaña **Consola** (*Console*).
4. Pega este comando y pulsa **Enter**:

   ```js
   copy(("; " + document.cookie).split("; MoodleSessionag=").pop().split(";").shift())
   ```

   La cookie queda **copiada en el portapapeles**; no se muestra nada en pantalla
   (o solo `undefined`), y es normal.

> **¿Chrome no te deja pegar?** La primera vez, Chrome muestra un aviso. Escribe
> `allow pasting` (o `permitir pegar`), pulsa Enter y vuelve a pegar el comando.
>
> **¿Se ha copiado algo vacío?** Hazlo a mano: en las herramientas de
> desarrollador ve a **Aplicación → Cookies → https://aulaglobal.uc3m.es**
> (en Firefox: **Almacenamiento → Cookies**) y copia el **valor** de la fila
> `MoodleSessionag`.

⚠️ Esa cookie da acceso a tu cuenta mientras la sesión esté abierta. No la
pegues en chats, foros ni en el propio asistente de IA.

### 2.2 Convertir la cookie en token

Vuelve al asistente y pega la cookie cuando te lo pida. No verás lo que pegas.

<details>
<summary>Método manual</summary>

En Bash, dentro de la carpeta del proyecto:

```bash
mkdir -p secrets data && chmod 700 secrets data
IFS= read -rs -p 'Pega la cookie y pulsa Enter: ' cookie; printf '\n' >&2
printf '%s' "$cookie" | ./aulaglobal-mcp token-from-cookie --output secrets/token
unset cookie
```

Añade `--force` al subcomando para reemplazarlo. Si falla, vuelve a iniciar sesión
y copia una cookie nueva.

</details>

---

## Paso 3 · Conectarlo con tu asistente

### Claude Code

Crea un archivo llamado `.mcp.json` en la carpeta donde vayas a usar Claude Code
(por ejemplo, la propia carpeta `aulaglobal-mcp`) con este contenido, cambiando
`/ruta/a/aulaglobal-mcp` por la ruta real (la obtienes con el comando `pwd`):

```json
{
  "mcpServers": {
    "aulaglobal": {
      "command": "/ruta/a/aulaglobal-mcp/aulaglobal-mcp",
      "args": ["--transport", "stdio"],
      "env": {
        "AULAGLOBAL_TOKEN_FILE": "/ruta/a/aulaglobal-mcp/secrets/token",
        "AULAGLOBAL_DOWNLOAD_DIR": "/ruta/a/aulaglobal-mcp/data"
      }
    }
  }
}
```

Abre Claude Code en esa carpeta (`claude`), acepta el servidor `aulaglobal`
cuando te lo pregunte y escribe `/mcp` para comprobar que aparece como
conectado.

### Codex

Añade este bloque a `~/.codex/config.toml`, cambiando las rutas por las reales
(usa `pwd` para conocerlas):

```toml
[mcp_servers.aulaglobal]
command = "/ruta/a/aulaglobal-mcp/aulaglobal-mcp"
args = ["--transport", "stdio"]
env = { AULAGLOBAL_TOKEN_FILE = "/ruta/a/aulaglobal-mcp/secrets/token", AULAGLOBAL_DOWNLOAD_DIR = "/ruta/a/aulaglobal-mcp/data" }
```

O regístralo desde la terminal en lugar de editar el archivo:

```bash
codex mcp add aulaglobal \
  --env AULAGLOBAL_TOKEN_FILE=/ruta/a/aulaglobal-mcp/secrets/token \
  --env AULAGLOBAL_DOWNLOAD_DIR=/ruta/a/aulaglobal-mcp/data \
  -- /ruta/a/aulaglobal-mcp/aulaglobal-mcp --transport stdio
```

Comprueba el registro con `codex mcp list`. Abre Codex y usa `/mcp` para comprobar
la conexión; si ya estaba abierto, reinícialo.

### Claude Desktop u otros clientes

Usa el mismo bloque `"aulaglobal": { ... }` dentro de la sección `mcpServers` del
archivo de configuración de tu cliente (en Claude Desktop: *Ajustes → Desarrollador
→ Editar configuración*) y reinicia el cliente.

---

## Paso 4 · Usarlo

Pídeselo con tus palabras. Para empezar, prueba:

1. «Comprueba mi conexión con AulaGlobal» → debería decir tu nombre.
2. «Lista mis asignaturas.»
3. «¿Qué archivos hay en *nombre de asignatura*?»
4. «Descarga los PDF de *asignatura* y resúmeme el primero.»
5. «¿Qué entregas tengo pendientes y cuáles ya he entregado?»

Los archivos descargados quedan en la carpeta `data/`, ordenados así:

```
data/
└── Nombre de la asignatura/
    ├── Tema 1/
    │   └── apuntes.pdf
    └── Entregas/
        └── Práctica 1/
            └── enunciado.pdf
```

Si vuelves a descargar, solo se bajan los archivos nuevos o modificados.

### Qué puede hacer el asistente

| Herramienta | Para qué sirve |
|---|---|
| `auth_status` | Comprobar que el token funciona y desde cuándo lo tienes |
| `list_courses` | Ver tus asignaturas (actuales, pasadas o futuras) |
| `list_course_files` | Ver los archivos de una asignatura |
| `download_files` | Descargar archivos o una asignatura entera |
| `read_file` | Leer un archivo descargado (PDF, Word, PowerPoint, texto…) |
| `search_files` | Buscar un texto en todo lo descargado |
| `list_assignments` | Ver entregas: fechas, enunciado y, si quieres, tu estado y nota |

---

## Problemas frecuentes

**«token invalid or expired»**
El token ha caducado o no es válido. Moodle puede hacer caducar los tokens
(por defecto, a las 12 semanas). Vuelve a ejecutar `./obtener-token.sh` y acepta reemplazar el token.

**«cannot read token file»**
No existe `secrets/token` o la ruta del `.mcp.json` está mal. Revisa que la ruta
sea absoluta (empieza por `/`) y que hiciste el Paso 2.

**El servidor no aparece en `/mcp`**
Comprueba la ruta de `command` en `.mcp.json` y que el archivo `aulaglobal-mcp`
existe y es ejecutable (`ls -l aulaglobal-mcp`). Reinicia Claude Code.

**Un PDF no se puede leer**
Los PDF escaneados (imágenes) no tienen texto y no se pueden leer: no hay OCR.
Si es muy grande, el asistente puede recibir «extraction in progress»: vuelve a
pedírselo en unos segundos.

---

## Seguridad en pocas palabras

- El token se guarda solo en `secrets/token`, con permisos que solo tú puedes
  leer. **No lo compartas ni lo subas a Git** (la carpeta ya está en `.gitignore`).
- El asistente nunca recibe el token: el programa lo usa por dentro y lo borra de
  cualquier mensaje de error.
- El programa solo puede escribir y leer dentro de la carpeta `data/`.
- El contenido de los cursos se trata como datos: si un PDF contiene
  instrucciones raras, el asistente no debe obedecerlas.

---

## Avanzado: servidor HTTP con Docker

Si prefieres tenerlo como servicio (por ejemplo, para varios clientes en tu
máquina):

```bash
docker build -t aulaglobal-mcp:local .

# Contraseña para el servidor MCP (distinta de tu token de AulaGlobal)
export MCP_HTTP_AUTH_TOKEN="$(openssl rand -hex 32)"
echo "$MCP_HTTP_AUTH_TOKEN"   # guárdala: la necesita tu cliente

docker run -d --name aulaglobal --user "$(id -u):$(id -g)" \
  -p 127.0.0.1:8080:8080 \
  -e MCP_HTTP_AUTH_TOKEN \
  -e AULAGLOBAL_TOKEN_FILE=/run/secrets/token \
  -v "$PWD/secrets/token:/run/secrets/token:ro" \
  -v "$PWD/data:/data" \
  aulaglobal-mcp:local
```

En tu cliente MCP configura la URL `http://127.0.0.1:8080/mcp` y la cabecera
`Authorization: Bearer <tu MCP_HTTP_AUTH_TOKEN>`. Si usas otro puerto, por
ejemplo `-p 127.0.0.1:9000:8080`, añade también
`-e MCP_ALLOWED_HOSTS=127.0.0.1:9000,localhost:9000`.

En Codex, usa este bloque en `~/.codex/config.toml` en lugar del bloque stdio:

```toml
[mcp_servers.aulaglobal]
url = "http://127.0.0.1:8080/mcp"
bearer_token_env_var = "MCP_HTTP_AUTH_TOKEN"
```

Inicia Codex desde la terminal donde exportaste `MCP_HTTP_AUTH_TOKEN`.
También puedes registrarlo con
`codex mcp add aulaglobal --url http://127.0.0.1:8080/mcp --bearer-token-env-var MCP_HTTP_AUTH_TOKEN`.
Esta contraseña es la del servidor MCP, distinta del token de AulaGlobal.

Para pararlo: `docker stop aulaglobal`.

---

## Más información

Configuración completa (variables de entorno, límites), detalles de seguridad,
caducidad del token y desarrollo: [docs/TECNICO.md](docs/TECNICO.md).
