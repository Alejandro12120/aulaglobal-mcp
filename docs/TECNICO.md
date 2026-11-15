# AulaGlobal MCP · Referencia técnica

Servidor Go ligero para consultar asignaturas y descargar/leer sus documentos en
AulaGlobal UC3M. SDK oficial MCP Go, stdio o Streamable HTTP en /mcp.
Go **1.26.9** fijado (incluido el contenedor de compilación); no requiere Go en el host.

## Token

No se aceptan credenciales por herramientas MCP. Configura **una** de
AULAGLOBAL_TOKEN o AULAGLOBAL_TOKEN_FILE (el archivo tiene prioridad).
Mantén los archivos de secretos con permisos 0600 y directorios 0700.

Para convertir una cookie MoodleSessionag de tu propia sesión, copia **solo su
valor** desde las herramientas del navegador. En Bash, sin eco ni historial del
valor, usando el binario compilado:

```bash
IFS= read -rs -p 'MoodleSessionag: ' cookie
printf '\n' >&2
printf '%s' "$cookie" | ./aulaglobal-mcp token-from-cookie --output secrets/token
unset cookie
```

El subcomando lee stdin (rechaza terminales para evitar eco), no argv; no sigue
redirecciones, exige Location moodlemobile://token=..., decodifica base64 y
verifica MD5(siteurl+passport) usando un passport aleatorio. Nunca imprime el
token. Crea archivos nuevos con 0600; usa `--force` para sustituirlos atómicamente dentro de un directorio privado. Si existe
privatetoken lo guarda en secrets/token.private; no se necesita para este
servidor. Guarda también `<tokenfile>.meta.json` (0600), con `obtained_at` RFC3339 sin secretos. `auth_status` lo lee junto a AULAGLOBAL_TOKEN_FILE y devuelve `token_obtained_at` y `token_age_days`. No lo compartas. Puede que Moodle exija volver a iniciar sesión o una
confirmación: el subcomando devuelve un error, no automatiza SSO.

Para compilar el binario sin instalar Go:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.26.9   sh -c 'CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o aulaglobal-mcp ./cmd/aulaglobal-mcp'
```

## Stdio

```bash
AULAGLOBAL_TOKEN_FILE="$PWD/secrets/token" ./aulaglobal-mcp --transport stdio
```

Claude Code (también formato habitual en clientes MCP; sustituye rutas absolutas):

```json
{
  "mcpServers": {
    "aulaglobal": {
      "command": "/ruta/aulaglobal-mcp",
      "args": ["--transport", "stdio"],
      "env": {
        "AULAGLOBAL_TOKEN_FILE": "/ruta/secrets/token",
        "AULAGLOBAL_DOWNLOAD_DIR": "/ruta/documentos"
      }
    }
  }
}
```

Cliente genérico mediante Docker, sin pseudo-TTY:

```json
{
  "mcpServers": {
    "aulaglobal": {
      "command": "docker",
      "args": ["run", "--rm", "-i",
        "-v", "/ruta/secrets/token:/run/secrets/token:ro",
        "-v", "ag-data:/data",
        "-e", "AULAGLOBAL_TOKEN_FILE=/run/secrets/token",
        "aulaglobal-mcp:local", "--transport", "stdio"]
    }
  }
}
```

El UID 65532 del contenedor debe poder leer los secretos: para archivos 0600,
utiliza propietario 65532 o ejecuta con tu UID y un directorio de datos escribible.

## HTTP con Docker

```bash
docker build -t aulaglobal-mcp:local .
# MCP_HTTP_AUTH_TOKEN debe contener un secreto aleatorio independiente del token Moodle.
docker run --rm --name aulaglobal -p 127.0.0.1:8080:8080   -e MCP_HTTP_AUTH_TOKEN   -e AULAGLOBAL_TOKEN_FILE=/run/secrets/token   -v /ruta/secrets/token:/run/secrets/token:ro   -v ag-data:/data aulaglobal-mcp:local
```

Configura el cliente HTTP con URL http://127.0.0.1:8080/mcp y
Authorization: Bearer <secreto MCP>. Transporte stateless, sin sesiones persistentes.
Al mapear un puerto distinto, Host contiene el puerto del **host**:
```bash
docker run --rm -p 127.0.0.1:9000:8080 -e MCP_ALLOWED_HOSTS=127.0.0.1:9000,localhost:9000 -e MCP_HTTP_AUTH_TOKEN -e AULAGLOBAL_TOKEN_FILE=/run/secrets/token -v /ruta/secrets/token:/run/secrets/token:ro -v ag-data:/data aulaglobal-mcp:local
```
Configura el cliente con http://127.0.0.1:9000/mcp. MCP_ALLOWED_HOSTS es una
lista de valores Host exactos separados por comas; no uses comodines.
Si no se configura, acepta localhost y 127.0.0.1 con el puerto de escucha.

El contenedor escucha 0.0.0.0 internamente para el port mapping; el binario
por defecto escucha 127.0.0.1. Expón el puerto solo en loopback o mediante un
proxy TLS autenticado. Todos los usuarios con el bearer comparten datos y token.

## Herramientas y contratos

Todas publican inputSchema/outputSchema tipados y devuelven JSON estructurado.
Los errores globales son errores de herramienta MCP; los errores individuales
de descarga se devuelven en results.

| Herramienta | Entrada | Salida |
|---|---|---|
| auth_status | {} | fullname, userid, sitename, release, expires_at nullable, expiry_source, token_obtained_at/token_age_days opcionales, update_available opcional |
| list_courses | classification: all/inprogress/past/future (inprogress), language: es/en (es) | courses: id, fullname, shortname, localized_name |
| list_course_files | course_id positivo, include/exclude: extensiones opcionales | sections → modules → contents: filename, filesize nullable, mimetype, timemodified, handle, section, module |
| download_files | handles[] de un listado anterior **o** course_id | results: path relativo, bytes, status downloaded/skipped/error, error opcional |
| read_file | path relativo, offset byte (0), length (16384; máx. 65536) | size, text, next_offset (-1 al terminar), note |
| search_files | query, regex (false), limit (20; 1..100), path_prefix opcional | matches: path, page (si se conoce), offset dentro de página, snippet |
| list_assignments | course_ids opcional (cursos inprogress), status upcoming/overdue/all, include_submission_status (false), language es/en | assignments: fechas Unix + RFC3339 Europe/Madrid, estado de apertura, intro no confiable, adjuntos con handles, estado de entrega opcional |

Usa list_courses → list_course_files → download_files → read_file.
Los handles se conservan en memoria (máximo 100000); vuelve a listar tras reiniciar.
Las rutas usan `<Asignatura localizada>/<Sección>/<archivo>`, conservando
subcarpetas filepath de los módulos carpeta, siempre sanitizadas.
Solo las colisiones añaden sufijos legibles (2), (3), etc., asignados en orden de handle y conservados en el manifiesto.
Cada curso usa un nombre local canónico en español, persistido por ID en el manifiesto incluso tras renombrarlo; el idioma de visualización no cambia la carpeta. Los nombres conocidos se reutilizan en memoria sin otra consulta de curso.
El manifiesto atómico `.aulaglobal-cache/manifest.json` registra handle estable
(hash de curso + módulo + filepath + filename), ruta, tamaño y timemodified.
Una revisión de URL no cambia el handle ni crea otra copia; skip funciona tras
reiniciar. Las rutas ya asignadas se conservan aunque cambie el nombre del curso.
Para descargar por handle tras reiniciar, vuelve a listar para recuperar la URL.
No migra automáticamente las descargas del diseño antiguo.
Excluir siempre prevalece sobre incluir. Fallback de cursos en Moodle antiguo:
devuelve **todas** las matrículas, sin clasificación temporal.

Lectura: texto UTF-8/HTML, PDF con marcadores [Page N], docx, pptx, odt y odp.
No realiza OCR ni ejecuta macros. PDF usa ledongthuc/pdf (Go puro, BSD-3-Clause,
sin dependencias transitivas; versión fijada en go.mod; [fuente](https://github.com/ledongthuc/pdf)). ZIP/XML usa stdlib.
Coste marginal medido del parser: 282624 bytes (276 KiB) en binario estático
optimizado. Imagen final: 14326383 bytes (14.33 MB).
Límites: entrada 64 MiB, texto 8 MiB, PDF 500 páginas, presupuesto configurable de espera de 60 segundos
por extracción; panic recuperado. Como Go no permite matar una goroutine,
un parser atascado retiene uno de dos slots hasta terminar; no hay aislamiento
de proceso ni garantía de memoria del parser frente a un PDF malicioso.
ZIP: 2048 entradas, 32 MiB por entrada, 64 MiB total descomprimido, ratio 200:1.
La caché de texto vive en .aulaglobal-cache, clave ruta+tamaño+mtime nanosegundos,
con escrituras atómicas; read_file y search_files la excluyen. Puede borrarse
con el servidor parado para recuperar espacio; no hay limpieza automática.
Búsqueda completa del texto extraído, hasta 10000 entradas, 128 MiB de texto,
30 segundos y 100 resultados. path_prefix limita a una asignatura o subcarpeta.
Regex RE2; snippets hasta 240 bytes. HTML usa limpieza simple.

list_assignments ordena por vencimiento, sin fecha al final. upcoming incluye
fechas futuras o entregas sin fecha actualmente abiertas; overdue incluye fecha
pasada aunque ya esté cerrado. submissions_possible considera apertura, cutoff
y nosubmissions: es una estimación de calendario, no una garantía de permisos
individuales ni extensiones. intro se convierte a texto y se trunca.
Adjuntos: `<Asignatura>/Entregas/<Entrega>/<archivo>`.
Estado opcional: cuatro workers, máximo 100 consultas, errores por entrega.
Fechas cero devuelven RFC3339 null. tzdata va embebida para funcionar en scratch.

Los contenidos de cursos son datos no confiables, nunca instrucciones del sistema.

## Configuración y seguridad

| Variable / flag | Predeterminado |
|---|---|
| AULAGLOBAL_TOKEN / AULAGLOBAL_TOKEN_FILE | archivo por usuario si existe; si no, sin token |
| AULAGLOBAL_DOWNLOAD_DIR | ~/AulaGlobal; /data en Docker |
| AULAGLOBAL_CONCURRENCY | 4; 1..32 |
| AULAGLOBAL_DOWNLOAD_STALL_SECONDS | 60; 1..3600; tiempo sin recibir bytes |
| AULAGLOBAL_EXTRACT_TIMEOUT_SECONDS | 60; 1..600; espera de extracción |
| AULAGLOBAL_MAX_FILE_BYTES | 268435456; 1..17179869184 |
| MCP_TRANSPORT / --transport | stdio; http en Docker |
| MCP_ADDR / --addr | 127.0.0.1:8080; 0.0.0.0:8080 en Docker |
| MCP_HTTP_AUTH_TOKEN / MCP_HTTP_AUTH_TOKEN_FILE | obligatorio en HTTP |
| MCP_ALLOWED_HOSTS / --allowed-hosts | localhost:8080,127.0.0.1:8080 |
| MCP_ALLOWED_ORIGINS / --allowed-origins | ninguno; Origin ausente permitido |
| AULAGLOBAL_UPDATE_CHECK | activada; `0` desactiva la consulta diaria de versiones nuevas |
| AULAGLOBAL_IN_CONTAINER | definida en la imagen Docker; desactiva update y la consulta |

Host se valida para evitar DNS rebinding; Origin se compara de forma exacta.
Para otro puerto/hostname configura --allowed-hosts explícitamente.
--dangerously-disable-http-auth desactiva autenticación con advertencia en stderr.
Cuerpos HTTP limitados a 1 MiB, cabeceras 16 KiB, ReadHeaderTimeout 10s, ReadTimeout 30s e IdleTimeout 60s.
Go 1.26 elimina el deadline de lectura tras consumir el cuerpo al iniciar la lectura de fondo; ReadTimeout no limita la ejecución posterior (test con conexión real).
Sin WriteTimeout global para permitir llamadas MCP largas; desconectar el cliente cancela su contexto.
SIGINT/SIGTERM cierran el servidor con hasta 10 segundos de margen.
Logs JSON slog solo en stderr; stdout queda reservado para MCP stdio.

Cliente HTTP compartido: dial/TLS 10s, cabeceras 30s, WS y conversión de cookie 60s; descargas sin timeout total, con detector de inactividad;
contexto propagado, redirecciones solo al mismo host/esquema (máximo 5).
POST form para WS; token query solo para pluginfile. Errores de red redactados,
mensajes Moodle arbitrarios no se propagan. Solo GET idempotentes reintentan
timeouts/5xx (3 intentos, backoff cancelable); no se reintenta POST.
Descargas verifican status, rechazan HTML inesperado y errores JSON, tamaño,
y escriben temporal exclusivo → fsync → rename. Skip compara tamaño y mtime.
os.Root confina lecturas/escrituras incluso ante enlaces simbólicos hacia fuera.
Usa un volumen dedicado; no permitas que procesos no confiables escriban en él.
Los archivos se crean 0600, directorios 0700; no se almacena una cookie.
La imagen scratch contiene únicamente binario estático, CA y /data, UID 65532;
incluye tzdata embebida (Europe/Madrid con horario de verano).

## Caducidad y futura renovación

No se debe asumir que un token móvil no caduca. En Moodle 4.5,
[generate_token_for_current_user](https://github.com/moodle/moodle/blob/MOODLE_405_STABLE/lib/external/classes/util.php)
asigna validuntil = timecreated + CFG->tokenduration a nuevos tokens y describe
12 semanas como valor por defecto. Puede reutilizar un token existente.
Esto **no demuestra** la política/version de UC3M ni la fecha de este token.

[get_site_info](https://github.com/moodle/moodle/blob/MOODLE_405_STABLE/webservice/externallib.php)
no expone validuntil.
[launch.php](https://github.com/moodle/moodle/blob/MOODLE_405_STABLE/admin/tool/mobile/launch.php)
envía firma:::token[:::privatetoken], sin fecha de creación/caducidad.
[get_config/get_public_config](https://github.com/moodle/moodle/blob/MOODLE_405_STABLE/admin/tool/mobile/classes/api.php)
no exponen en esa versión el validuntil del token; los ajustes de autologin no
son la caducidad del token WS. Por eso auth_status devuelve expires_at:null y
expiry_source:unknown..., nunca calcula 12 semanas desde la fecha de conversión.
invalidtoken/invalidtimedtoken indican token caducado; accessexception se confirma con
core_webservice_get_site_info: si también falla, el token está caducado (ErrTokenExpired,
mensaje en español con instrucciones de renovación); si funciona, es un problema de
permisos y el mensaje lo distingue. Con token caducado, download_files y
list_assignments abortan el lote con un único error en lugar de un error por elemento.

TokenProvider.Get(ctx) permite incorporar renovación sin cambiar API/herramientas;
Invalidator es una interfaz opcional. El proveedor estático relee archivos por
petición (rotación externa posible). Futuro: proveedor sincronizado que invalide
al recibir error de autenticación y use un flujo Moodle explícitamente compatible,
quizás el privatetoken para autologin. **No existe aquí una renovación automática**
ni se considera privatetoken un refresh token OAuth.
Si la política tokenduration del sitio caduca el token (12 semanas por defecto
upstream), ejecuta de nuevo `aulaglobal-mcp setup`: valida el token guardado y, si ha
caducado, salta directamente al paso de la cookie. `token-from-cookie --force` sigue
disponible como alternativa de bajo nivel. No hace falta reiniciar el asistente
(el archivo del token se relee en cada petición). obtained_at registra
la conversión, no la creación del token; expires_at sigue siendo null.

## Desarrollo y comprobación

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.26.9 go vet ./...
docker run --rm -v "$PWD":/src -w /src golang:1.26.9 go test -race ./...
docker build -t aulaglobal-mcp:local .
node scripts/smoke.mjs
docker image inspect aulaglobal-mcp:local --format '{{.Size}}'
```

Pruebas sin credenciales reales: httptest Moodle, errores JSON HTTP 200, token
inválido, redacción, redirects de launch, filtros/nombres, 200 descargas con cuatro
workers, skip, cancelación, HTML/JSON/tamaño incorrectos, colisiones y escapes
por symlinks. scripts/smoke.mjs verifica ambos transportes y seguridad HTTP.

Estructura: cmd/aulaglobal-mcp (CLI/config/lifecycle),
internal/aulaglobal (API/token/cookie), internal/store (descarga/lectura/búsqueda),
internal/mcpserver (schemas/herramientas/seguridad HTTP).

Si vence la espera de extracción, read_file devuelve “extraction in progress, retry shortly”. El parser continúa en uno de los dos slots globales y guarda la caché al terminar con éxito, incluso si el cliente cancela o agota su plazo. Un parser bloqueado retiene su slot; no existe aislamiento de proceso ni límite duro de memoria. Se mantiene el rechazo de PDFs de más de 500 páginas para no presentar contenido incompleto. Las introducciones de entregas se limitan a 2000 runas más “ [truncated]”.

## Guía rápida: servidor HTTP con Docker

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

## ChatGPT en la web

**La conexión web es distinta.** Según el [Centro de ayuda de OpenAI](https://help.openai.com/en/articles/12584461-connecting-custom-mcp-servers-in-chatgpt),
las conexiones personalizadas web están disponibles en Business y Enterprise/Edu
con permisos del administrador. Se añaden desde **Plugins → + → Add custom MCP
server**, con nombre, descripción opcional, URL HTTPS pública (o Secure MCP
Tunnel) y autenticación. Esa vía no lee tu configuración local. Para empezar,
usa stdio en el escritorio: evita exponer tus archivos de cursos y el acceso a
AulaGlobal a través de una URL pública. Nunca publiques este servidor con la
autenticación HTTP desactivada.

Fuentes consultadas el 14 de noviembre de 2026; los menús pueden cambiar.


## Instalación nativa y compatibilidad

`aulaglobal-mcp setup [--secrets-dir RUTA] [--data-dir RUTA] [--no-pause]`
usa entrada oculta con x/term y verifica con Auth, sin curl. Espera Enter al
terminar si stdin es terminal; --no-pause lo evita. En Windows, sin argumentos
y con terminal, abre setup para permitir doble clic.

Secretos predeterminados (os.UserConfigDir()/aulaglobal-mcp/token):
- Linux: $XDG_CONFIG_HOME/aulaglobal-mcp/token o ~/.config/aulaglobal-mcp/token.
- macOS: ~/Library/Application Support/aulaglobal-mcp/token.
- Windows: %APPDATA%/aulaglobal-mcp/token.

Apuntes: os.UserHomeDir()/AulaGlobal en los tres sistemas. No depende del cwd.
AULAGLOBAL_TOKEN_FILE y AULAGLOBAL_DOWNLOAD_DIR conservan prioridad sobre estos
valores. En setup los flags tienen prioridad sobre las variables.
AULAGLOBAL_TOKEN sigue funcionando en el servidor; setup obtiene un token nuevo
a partir de una cookie, no usa esa variable. token-from-cookie mantiene su salida
histórica secrets/token. Para conservar instalaciones antiguas configura
explícitamente AULAGLOBAL_TOKEN_FILE=/ruta/secrets/token y
AULAGLOBAL_DOWNLOAD_DIR=/ruta/data. No se trasladan archivos automáticamente.

Unix exige directorio privado 0700 y archivos 0600. Windows omite la comprobación
de bits POSIX; depende de las ACL privadas del perfil. No elijas carpetas
compartidas. SaveSecret y las configuraciones usan temporal, Sync, Close y Rename.
Go en Windows usa MoveFileEx con REPLACE_EXISTING; sustituye destinos existentes,
pero la documentación de os.Rename no garantiza atomicidad en sistemas no Unix.
No se promete una transacción entre token, metadatos y token privado.
Solo se limpian token.meta.json, token.private y .token-* si falta token.

Claude JSON se combina conservando valores ajenos (incluso números grandes).
TOML usa github.com/pelletier/go-toml/v2 solo para validar y serializar el bloque
nuevo: el resto se conserva al añadir. Se pide confirmación antes de sustituir.
La sustitución conservadora rechaza texto multilínea o tablas inline ambiguas;
en esos casos se edita a mano. Se usa el parser pequeño para no aceptar TOML
inválido ni duplicar tablas con claves entrecomilladas. Copia .bak antes de
sustituir; escritura con temporal en el mismo directorio.
Detección de clientes por sus carpetas de configuración; si todavía no existen,
abre primero la aplicación o usa los ejemplos manuales. No detecta cada variante
de empaquetado (por ejemplo, instalaciones sandbox).

Manifiestos nuevos guardan rutas con /; también se normalizan separadores de
manifiestos antiguos al leer. os.Root mantiene el confinamiento en cada OS.
Sanitize elimina caracteres reservados, puntos/espacios extremos y nombres DOS.
Skip compara segundos Unix exactos: en FAT con resolución de dos segundos puede
volver a descargar un archivo con fecha impar; es conservador y evita omitir
cambios. NTFS/APFS no necesitan tolerancia. Entradas interactivas aceptan CRLF.
SIGINT/SIGTERM compilan en Windows; el cierre por señal Unix requiere CI/host Unix.

## Actualización automática

`aulaglobal-mcp update [--check] [--yes] [--force]` está implementado en
internal/selfupdate (la CLI en cmd/aulaglobal-mcp/update.go).

Flujo: consulta `GET https://api.github.com/repos/Alejandro12120/aulaglobal-mcp/releases/latest`
(sin autenticación, User-Agent `aulaglobal-mcp/<versión>`; excluye borradores y
prereleases), compara versiones semver estrictas (`vX.Y.Z`; nunca se baja de
versión) y, si hay una más reciente y el usuario confirma ([S/n], o `--yes`):

1. Descarga `checksums.txt` y `checksums.txt.sig` y verifica la firma con la
   clave embebida **antes** de fiarse de nada más.
2. Descarga el archivo de la plataforma actual
   (`aulaglobal-mcp_<ver>_<os>_<arch>.tar.gz`, zip en Windows; mismo
   name_template que .goreleaser.yaml) y comprueba su SHA-256 contra la línea
   exacta de `checksums.txt`.
3. Extrae únicamente la entrada `aulaglobal-mcp` / `aulaglobal-mcp.exe` de la
   raíz del archivo (coincidencia exacta de nombre; no se escribe nada por el
   nombre de la entrada), con tope de 100 MiB.
4. Escribe un temporal en el mismo directorio que el ejecutable real
   (os.Executable + EvalSymlinks), `chmod 0755` y lo sustituye. Unix: rename
   atómico sobre el binario. Windows: el ejecutable en uso se renombra a
   `<nombre>.old`, el nuevo se mueve a su sitio y, si falla, se restaura el
   anterior. Cualquier comando elimina en el siguiente arranque un `.old`
   sobrante (si sigue bloqueado, se ignora). Si el directorio no es escribible,
   el error lo indica y no queda estado parcial.

Límites: 1 MiB para el JSON y checksums.txt, 16 KiB para la firma, 100 MiB por
archivo; timeouts de 15 s (API), 30 s (checksums/firma) y 10 min (archivo);
las redirecciones se limitan a 5 y nunca bajan de https a http. Las URL de
descarga se construyen a partir de la etiqueta validada, no se toman del JSON.
`update` se niega a ejecutarse dentro de un contenedor (`AULAGLOBAL_IN_CONTAINER`
o `/.dockerenv`; el Dockerfile lo define) y con versión `dev` o no comparable
salvo `--force`. No hay ninguna herramienta MCP que actualice.

### Firma (OpenSSH SSHSIG)

La clave pública de firma está en internal/selfupdate/update_key.pub (se
embebe con go:embed y la lee también release.yml). La verificación se hace en Go
sin dependencias (sshsig.go): se decodifica el bloque
`-----BEGIN SSH SIGNATURE-----`, se exige magic `SSHSIG`, versión 1, que la
clave incluida coincida byte a byte con la de confianza, namespace
`aulaglobal-mcp-update`, hash sha512 (o sha256) y se verifica con
crypto/ed25519 la firma sobre
`"SSHSIG" || string(namespace) || string(reserved) || string(hash_alg) || string(H(mensaje))`.
Los fixtures de internal/selfupdate/testdata los generó el `ssh-keygen` real con
una clave de prueba desechable.

### Aviso de versión nueva

Al arrancar el servidor (stdio o http) se lanza una única goroutine que, salvo
`AULAGLOBAL_UPDATE_CHECK=0`, contenedor o versión `dev`, lee la caché
`update-check.json` (`{"checked_at": RFC3339, "latest": "vX.Y.Z"}`) en
`os.UserCacheDir()/aulaglobal-mcp/`: `~/.cache` (Linux),
`~/Library/Caches` (macOS), `%LocalAppData%` (Windows). Si tiene menos de 24 h
se usa; si no, se consulta GitHub (5 s) y se reescribe la caché de forma
atómica con permisos 0600. Cualquier error se ignora (solo log debug). Las
llamadas a herramientas nunca esperan: leen el resultado en memoria.

Si hay una versión más reciente, auth_status añade `update_available` y el
primer resultado correcto de cualquier herramienta del proceso lleva un
contenido de texto adicional con un texto fijo en español en el que solo se
interpola la versión, validada como `vX.Y.Z`. Nunca se incluye texto remoto
(notas de versión, etc.), para evitar inyección de instrucciones.

### Mantenimiento: clave de firma

```sh
ssh-keygen -t ed25519 -N "" -C aulaglobal-mcp-update -f aulaglobal-update
```

- Guarda el contenido de `aulaglobal-update` (clave privada) como secreto del
  repositorio `UPDATE_SIGNING_KEY` y borra el archivo local.
- Sustituye el contenido de internal/selfupdate/update_key.pub por
  `aulaglobal-update.pub` y publica una versión con ese cambio.
- release.yml escribe el secreto en un archivo 0600 bajo `$RUNNER_TEMP`, falla si
  está vacío o si su clave pública no coincide con update_key.pub, y GoReleaser
  firma `checksums.txt` con `ssh-keygen -Y sign -n aulaglobal-mcp-update`
  (genera `checksums.txt.sig`). Tras publicar, un paso comprueba la firma con
  `ssh-keygen -Y verify` y un allowed_signers construido desde update_key.pub.
- Rotación: los binarios ya instalados solo confían en la clave con la que se
  compilaron. Para rotar, publica primero una versión firmada con la clave
  **antigua** que embeba la clave nueva; las siguientes pueden firmarse con la
  nueva (en esa versión de transición hay que relajar temporalmente la
  comprobación de coincidencia clave-secreto de release.yml). Si la clave
  se pierde o se compromete, los usuarios deben descargar manualmente una
  versión nueva. Las compilaciones locales con GoReleaser
  requieren `--skip=sign`.

## Compilar tú mismo y publicar versiones

Para compilar directamente usa el comando de la sección Token. El usuario de las
Releases no necesita Docker.

La CI ejecuta vet y pruebas nativas en Ubuntu, macOS y Windows; race en Unix.
Para publicar, configura primero el remoto de GitHub y sustituye el usuario de
los enlaces README. Después, desde una rama comprobada y con todos los cambios
confirmados:

```sh
git tag v0.1.0
git push origin v0.1.0
```

release.yml ejecuta GoReleaser v2.8.2 y publica seis archivos con checksums.txt y checksums.txt.sig (ver Actualización automática).
Compila con CGO_ENABLED=0, trimpath y main.version; --version imprime la versión.
No publica imágenes GHCR ni attestations por ahora. Los binarios aún no tienen
firma de sistema operativo (la firma de checksums.txt solo protege la actualización): quedan pendientes Authenticode, firma/notarización de Apple y provenance.
El proyecto usa licencia MIT (archivo LICENSE, incluido en cada archivo de release).



### Presentación y registro de Claude Code en setup

El instalador separa carpetas, token, comprobación y asistentes en cuatro pasos.
La entrada de cookie sigue oculta; también comprueba el token conservado.
Los encabezados usan negrita solo con stdout terminal y NO_COLOR ausente;
Windows usa texto sin códigos ANSI.

Claude Code se busca en PATH y en ~/.claude/local, ~/.local/bin,
/opt/homebrew/bin y /usr/local/bin. En Windows busca .exe y .cmd
en el perfil y %APPDATA%/npm. Cada llamada tiene un límite de 30 segundos
y captura los diagnósticos sin mostrarlos para evitar filtrar datos.
Se consulta mcp get aulaglobal antes de registrar; solo se sustituye una
entrada identificada como de usuario, tras confirmar y quitarla con scope user.
El ámbito local se puede mover a usuario: remove --scope local y add --scope user,
en el mismo cwd. Para proyecto solo se añade usuario; nunca se edita .mcp.json.
Un ámbito desconocido requiere revisión manual.
El alta usa mcp add --scope user aulaglobal [opciones -e] -- BINARIO
--transport stdio. Las opciones -e solo contienen rutas distintas de las
predeterminadas, nunca la cookie ni el token. El ejecutor es inyectable.
Los fallos muestran un comando manual inmediatamente debajo; no se imprime JSON de rutina.
Las pruebas usan un Moodle falso y un ejecutor simulado, sin servicios reales.

### Registro de OpenCode

Fuentes consultadas el 14-11-2026:
- https://opencode.ai/docs/mcp-servers/
- https://opencode.ai/docs/config/

V2 usa `mcp.servers.aulaglobal` sin `enabled` (para desactivar usa
`disabled`). V1 usa `mcp.aulaglobal` con `enabled: true`.
El fallback reconoce V1 por entradas con type; conserva V2 si existe servers.
Los archivos nuevos o vacíos usan V2 y $schema. environment solo contiene
rutas no predeterminadas; nunca se pasan cookies ni tokens en argv.

Se busca el ejecutable en PATH, ~/.opencode/bin, ~/.local/bin, npm global,
Homebrew y las variantes .exe/.cmd de Windows. El shim npm .cmd se ejecuta
con Node, sin shell. Cada llamada tiene un límite de 20 segundos.
Primero se prueba `opencode mcp add --help`: solo si admite --global
se ejecuta `opencode mcp add aulaglobal --global [--env K=V] -- EXE --transport stdio`.
Se comprueba antes la configuración global y se verifica después leyendo el
archivo global; una salida exitosa por sí sola no confirma el alta.
Para reemplazar se pide permiso y se edita solo esa entrada mediante el merge
seguro, sin quitarla antes ni depender de un comando remove.

La ruta global es ~/.config/opencode/opencode.json(c), con XDG_CONFIG_HOME
absoluto como alternativa. Si no hay CLI scriptable, debe existir la carpeta.
Se rechazan ambos archivos simultáneos, JSONC con comentarios/comas finales,
JSON inválido y estructuras ambiguas. Se muestran bloques manuales V1 y V2.
Se preservan valores desconocidos y servidores ajenos, reformateando JSON.
El original exacto se guarda en .bak; la escritura usa temporal y rename.
No se imprime la salida del CLI ni errores con posible contenido privado.

Prueba real en Docker node:22: npm opencode-ai 1.19.5 ofrece --env pero no
--global en mcp add --help. Las capturas están en cmd/aulaglobal-mcp/testdata.
Por ello esa versión usa el fallback; no se presupone que npm y la
documentación V2 estén sincronizados. No se contacta con UC3M.

## Antigravity: registro de setup

Investigación: 2026-11-14. La documentación actual sitúa la configuración global
compartida por App, IDE y CLI en `~/.gemini/config/mcp_config.json` (macOS y Linux),
es decir, `%USERPROFILE%\.gemini\config\mcp_config.json` en Windows. La ruta
antigua `~/.gemini/antigravity/mcp_config.json` ya no es el destino de escritura.
La equivalencia Windows se deriva del directorio de usuario; no se ejecutó la aplicación Windows.
No se modifican archivos locales `.agents/mcp_config.json`.

El esquema es un objeto `mcpServers` de entradas con `command`, `args` y `env`
para stdio. También admite `serverUrl` (alias `url`) para Streamable HTTP,
`cwd`, `headers`, `authProviderType`, `oauth`, `disabled`, `enabledTools`,
`disabledTools`, `tools` y `timeoutSeconds`. No se generan opciones remotas.
La documentación describe JSON, sin garantizar comentarios/JSONC: setup rechaza
comentarios, comas finales, claves duplicadas, raíces no objeto y `mcpServers`
no objeto. Imprime únicamente un ejemplo nuevo, nunca el contenido ni errores
privados del archivo existente o del CLI.

La detección usa lookup/stat inyectables: `agy` o `antigravity` en PATH,
directorios `.gemini/antigravity` o `.gemini/config`, `/Applications/Antigravity.app`
y `~/Applications/Antigravity.app`, instalaciones Linux `/usr/share/antigravity`
y `/opt/Antigravity`, y `%LOCALAPPDATA%\Programs\Antigravity\Antigravity.exe`
en Windows. Son heurísticas, no una garantía de descubrir instalaciones portables.
También busca `~/.local/bin/agy` y `%LOCALAPPDATA%\agy\bin\agy.exe`.
Los tests simulan metadatos, sin depender de permisos ejecutables del host.

Se probó el binario oficial Linux `agy 1.5.9`, instalado con el instalador oficial
en Docker: `--version`, `--help`, `mcp --help`, `mcp add --help` y un registro
stdio con entorno. La ayuda real se conserva en
`cmd/aulaglobal-mcp/testdata/agy-1.5.9-mcp-add-help.txt`.
`agy mcp add` acepta `-e KEY=value` antes del nombre y genera `disabled: false`.
Setup prueba esa ayuda y ejecuta argv sin shell en un HOME/USERPROFILE temporal;
lee la entrada generada y hace la fusión final con los helpers existentes.
Sin `agy`, genera directamente la entrada documentada. Un CLI incompatible o un
fallo deja la configuración intacta y muestra la alternativa manual.

La fusión conserva las demás claves y servidores mediante `json.RawMessage`
(incluidos números grandes); puede cambiar el formato y el orden de claves.
Antes de reemplazar `aulaglobal` pide confirmación. Antes de escribir guarda los
bytes originales en `.bak` y escribe con temporal + rename. Solo añade `env`
para las rutas que difieren de los valores predeterminados de setup.
Tras guardar, recuerda recargar desde `/mcp` en CLI, Restart en
Customizations → Installed → MCP Servers en App o reabrir/reiniciar IDE.
No se probó el refresco visual en una aplicación real.

Fuentes oficiales consultadas:

- https://antigravity.google/docs/mcp — ruta actual, esquema, interfaz y recarga CLI/App.
- https://antigravity.google/docs/getting-started — instalación oficial de `agy` y ubicaciones.
- https://antigravity.google/docs/changelog — notas vigentes de App, CLI e IDE.
- https://antigravity.google/cli/install.sh — instalador empleado en el contenedor.

El esquema multiplataforma y la interfaz gráfica están verificados documentalmente;
la generación de configuración y ayuda se verificaron también con el binario Linux.

## Registro unificado de asistentes

`setup_clients.go` define `assistant`: ID, Name, Detect, Inspect, Apply,
Manual y Reload. La única lista ordenada `assistants` sirve para detección,
selección por ID, ayuda y pruebas de contrato. Los estados son no instalado,
falta, idéntico, diferente y otro ámbito. Los errores de inspección se cierran
sin escribir ni mostrar diagnósticos privados.

`configureClients` inspecciona antes de preguntar, muestra el inventario y usa
siempre [S/n] para añadir o actualizar. Con dos o más cambios propone configurar
todos; `e` permite elegir y `n` omite todos los pendientes. Las entradas idénticas
no preguntan. El resumen distingue añadido, actualizado, ya estaba, omitido y
falló, y solo imprime Reload de los clientes modificados. Los transcriptos en
`cmd/aulaglobal-mcp/testdata/setup-*.golden` fijan el formato.

Para añadir un asistente, sigue **3 pasos**:

1. Crea un archivo que implemente `assistant` (o construya un `fileAssistant`
   con las particularidades del formato, ruta, detección y recarga).
2. Añade una línea a `assistants`. Su ID queda disponible en --help y --clients.
3. Ejecuta las pruebas compartidas `TestAssistantContracts` y añade fixtures o
   pruebas de las particularidades del nuevo cliente. No necesita otro flujo UI.

`clientEnv` inyecta lookup/stat, ejecutores de CLI con salida capturada,
generador agy y escritura. `desired` construye una ruta absoluta al ejecutable y
solo añade env para token/descargas distintos de los valores predeterminados.
`fileAssistant` comparte inspección, igualdad semántica, merge seguro y backup
atómico. JSON rechaza claves duplicadas; TOML conserva comentarios ajenos y
rechaza sustituciones ambiguas. Incluso --yes deja intacto un archivo inseguro.
No hay transacción entre remove y add en Claude: si falla, se informa del fallo
y se imprime el comando para recuperar la entrada de usuario.

Particularidades mantenidas (fuentes oficiales en las secciones anteriores):

- Claude Code: CLI con --scope user; local se quita con --scope local en el mismo
  cwd; proyecto se conserva. Verificado con el CLI real en HOME y cwd temporales:
  `Scope: Local config (private to you in this project)`,
  `Scope: Project config (shared via .mcp.json)`,
  `Scope: User config (available in all your projects)` y
  `No MCP server named "aulaglobal"`. `mcp remove --help` confirma
  `-s, --scope <scope>`. Fuente: https://code.claude.com/docs/en/mcp y ayuda local.
- Claude Desktop: mcpServers JSON, ruta por sistema; cierra y abre la aplicación.
  Fuente: https://modelcontextprotocol.io/docs/develop/connect-local-servers.
- Codex / ChatGPT: ~/.codex/config.toml, mcp_servers; reemplazo sin duplicar la
  tabla padre que genera el serializador. Fuente: https://developers.openai.com/codex/mcp/.
- OpenCode: prueba --global en la ayuda antes de usar el CLI; verifica la entrada
  global escrita, conserva V1/V2, rechaza dos archivos simultáneos y JSONC ambiguo.
- Antigravity: ~/.gemini/config/mcp_config.json; agy genera en HOME temporal y el
  merge escribe el destino. disabled:false equivale al valor por defecto.

`setup --only-clients` omite token y red. `--clients` filtra IDs y rechaza
los desconocidos antes de realizar cambios. `--yes` no pregunta ni espera Enter;
con un token existente lo conserva y comprueba; sin token pide ejecutar primero
setup interactivo (o usar --only-clients). No altera token-from-cookie.

Validación real opcional: compila las pruebas con Docker y ejecuta el binario en
el host con `AULAGLOBAL_TEST_REAL_CLAUDE=1`; usa
`AULAGLOBAL_TEST_AGY=/ruta/agy` para el generador de Antigravity. Ambas pruebas
usan carpetas temporales, sin credenciales ni conexiones a UC3M.

La prueba real con rutas con espacios confirma que el nombre debe ir antes de
las opciones -e: el parser variádico de Claude puede consumir aulaglobal como
una variable si se coloca después de -e. El separador -- precede al ejecutable.
