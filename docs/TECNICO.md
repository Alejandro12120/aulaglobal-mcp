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
| auth_status | {} | token_valid, sitename, release, expires_at nullable, expiry_source, token_obtained_at/token_age_days opcionales, update_available opcional. **No** devuelve nombre ni ID de usuario (ver Privacidad y minimización de datos) |
| list_courses | classification: all/inprogress/past/future (inprogress), language: es/en (es) | courses: id, fullname, shortname, localized_name |
| list_course_files | course_id positivo, include/exclude: extensiones opcionales | sections con modules y contents anidados: filename, filesize nullable, mimetype, timemodified, handle, section, module |
| download_files | handles[] de un listado anterior **o** course_id | results: path relativo, bytes, status downloaded/skipped/error, error opcional |
| read_file | path relativo, offset byte (0), length (16384; máx. 65536) | size, text, next_offset (-1 al terminar), note |
| search_files | query, regex (false), limit (20; 1..100), path_prefix opcional | matches: path, page (si se conoce), offset dentro de página, snippet |
| list_assignments | course_ids opcional (cursos inprogress), status upcoming/overdue/all, include_submission_status (false), language es/en | assignments: fechas Unix + RFC3339 Europe/Madrid, estado de apertura, intro no confiable, adjuntos con handles, estado de entrega opcional |

El orden habitual es list_courses, list_course_files, download_files y read_file.
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
| AULAGLOBAL_MAX_RPS | 4; 0.1..100; peticiones por segundo a AulaGlobal (ráfaga = el doble) |
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
mensajes Moodle arbitrarios no se propagan. Las descargas (GET idempotentes) reintentan
timeouts/5xx (3 intentos, backoff cancelable). Los POST de WS solo se repiten tras 429/503 y solo si la función es de solo lectura (ver Acceso respetuoso a AulaGlobal).
Descargas verifican status, rechazan HTML inesperado y errores JSON, tamaño,
y escriben un temporal exclusivo, lo sincronizan con fsync y lo renombran. Skip compara tamaño y mtime.
os.Root confina lecturas/escrituras incluso ante enlaces simbólicos hacia fuera.
Usa un volumen dedicado; no permitas que procesos no confiables escriban en él.
Los archivos se crean 0600, directorios 0700; no se almacena una cookie.
La imagen scratch contiene únicamente binario estático, CA, /data y /licenses (LICENSE y THIRD_PARTY_NOTICES.md), UID 65532;
incluye tzdata embebida (Europe/Madrid con horario de verano).

## Acceso respetuoso a AulaGlobal

Todo el tráfico saliente hacia AulaGlobal (llamadas WS, descargas pluginfile y
launch.php) pasa por una única puerta por proceso
(`internal/aulaglobal/polite.go`). Complementa la concurrencia ya limitada
(AULAGLOBAL_CONCURRENCY) y no sustituye al respeto de las normas de uso de la
universidad.

**Limitador de ritmo.** Cubo de fichas (token bucket) con la reserva de hueco
futuro, de modo que los hilos esperan en orden. Por defecto 4 peticiones/s con
ráfaga de 8: una consulta interactiva (cursos, contenidos, tareas) no espera
nunca, y una descarga masiva o la consulta de estado de entregas queda en
240 peticiones por minuto, del orden de lo que genera un navegador, muy lejos de
una carga abusiva. Se configura con `AULAGLOBAL_MAX_RPS` (número decimal entre
0.1 y 100; la ráfaga es el doble, mínimo 1); un valor no válido impide arrancar.
Un curso de 200 archivos tarda unos 50 s con el valor por defecto. La espera
respeta la cancelación del contexto. Las redirecciones del mismo host (máximo 5)
no consumen ficha propia.

**429 y 503.** Se lee `Retry-After` (segundos o fecha HTTP); sin cabecera, 5 s
duplicando por intento con ±25 % de jitter; siempre se limita a 120 s (mínimo 1 s
si el servidor indica 0). La pausa es **global**: la fijan y la respetan todas
las goroutines (también las peticiones que ya estaban en cola), no solo la que
recibió el error. Se reintenta como máximo 2 veces (3 intentos) y solo si la
petición es de solo lectura: GET de descarga y launch.php, y funciones WS de la
lista cerrada `readOnlyFns` (todas las que usa el cliente leen; ninguna escribe
en Moodle). Una función WS que no esté en esa lista nunca se reintenta, ni se
deduplica ni se cachea. Si la pausa es mayor que el tiempo que le queda al
llamante, o se agotan los reintentos, la herramienta devuelve «AulaGlobal está
limitando las peticiones; vuelve a intentarlo en N s.» (`errors.Is(err,
aulaglobal.ErrRateLimited)`). Una descarga por lotes o el estado de entregas
se detiene entera con ese único error en vez de seguir golpeando; lo ya
descargado se omite al repetir. Los errores JSON con HTTP 200 (errorcode de
Moodle) **no** son límites de ritmo y no se reintentan.

**Deduplicación.** Las llamadas WS de solo lectura idénticas (misma función,
mismos parámetros, mismo token) que coinciden en vuelo comparten una sola
petición (implementación propia estilo singleflight). La petición compartida no
depende de que un llamante cancele: se cancela solo cuando se van todos, y un
llamante vivo reintenta si la compartida murió por el plazo de otro.

**Caché breve.** Respuestas correctas de los listados `core_course_get_enrolled_courses_by_timeline_classification`,
`core_enrol_get_users_courses`, `core_course_get_contents`,
`core_course_get_courses_by_field` y `mod_assign_get_assignments`, 45 s de TTL,
máximo 32 entradas, 32 MiB en total y 8 MiB por entrada, solo en memoria. No se
cachean: `core_webservice_get_site_info` (auth_status y setup deben ver un token
muerto al instante), el estado de entrega (cambia al entregar), los errores de
cualquier tipo, las descargas ni la conversión de cookie en token. La clave es un
SHA-256 de token+función+parámetros: el token nunca aparece en claro en la
clave ni en logs, y cambiar de token cambia de clave.

## Privacidad y minimización de datos

Este programa solo habla con AulaGlobal (y, para la comprobación diaria de
versiones, con GitHub, sin datos del usuario ni del curso). Lo que devuelven las
herramientas MCP se entrega al asistente conectado, que puede enviarlo a su
proveedor de IA según sus condiciones; ni este programa ni sus restricciones
controlan eso. Las restricciones de carpeta (os.Root, rutas relativas) solo se
aplican a las herramientas de AulaGlobal MCP: un asistente con acceso propio al
sistema de archivos tiene los permisos que él mismo se conceda. `setup` muestra
este aviso antes de conectar asistentes.

Minimización: `auth_status` no devuelve el nombre completo ni el ID de usuario
(tipo `AuthOutput` propio en `internal/mcpserver`, separado de `aulaglobal.Auth`,
que sí los decodifica para uso local: el saludo de `setup` y el userid del
respaldo de matrículas en Moodle antiguo). Ninguna otra herramienta devuelve datos
de la cuenta; las notas/calificaciones solo salen si se pide
`include_submission_status`. El servidor MCP no define `instructions`, por lo que
no se añade texto de privacidad al prompt del asistente.

## Avisos de terceros

`THIRD_PARTY_NOTICES.md` (raíz; incluido en cada archivo de release y en
`/licenses` de la imagen Docker) contiene la licencia MIT de AGDownloader (partes
en las que se basa este proyecto), la de la biblioteca estándar de Go y el texto
completo de la licencia de cada módulo Go enlazado en el binario de las seis
plataformas. Se regenera con Docker (sin instalar nada en el host):

```sh
scripts/gen-third-party-notices.sh
```

El generador (`scripts/gen-notices`) lista los módulos con `go list -deps` para
linux, darwin y windows en amd64 y arm64, lee los LICENSE del caché de módulos y
comprueba que cada uno contiene el texto esperado para su SPDX; un módulo nuevo
sin SPDX registrado en `spdxOf` detiene la ejecución para que alguien lea su
licencia. La sección de AGDownloader se edita a mano en
`scripts/gen-notices/preamble.md`. El test `scripts/gen-notices` (parte de `go
test ./...`, solo en Linux; el resultado no depende del sistema operativo) falla
si un módulo compilado falta, sobra o cambió de versión respecto al archivo.
Verificación cruzada puntual: `go-licenses report` coincide con los SPDX.

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
scripts/test-install.sh   # instaladores, ver «Instaladores»; necesita Docker
docker image inspect aulaglobal-mcp:local --format '{{.Size}}'
```

Pruebas sin credenciales reales: httptest Moodle, errores JSON HTTP 200, token
inválido, redacción, redirects de launch, filtros/nombres, 200 descargas con cuatro
workers, skip, cancelación, HTML/JSON/tamaño incorrectos, colisiones y escapes
por symlinks. scripts/smoke.mjs verifica ambos transportes y seguridad HTTP.

Estructura del código:

- `cmd/aulaglobal-mcp`: la CLI. `main.go` solo reparte los subcomandos (añadir
  uno es un `case` y un archivo); `serve.go` arranca el servidor MCP;
  `setup*.go`, `uninstall*.go`, `update.go` y `token_from_cookie.go` son los
  subcomandos. Cada asistente de IA es un archivo `assistant_<id>.go`;
  `assistant.go` define la interfaz y la lista, y `config_json.go` y
  `config_write.go` la edición de configuraciones que comparten.
- `internal/aulaglobal`: API de Moodle, token, cookie y acceso respetuoso.
- `internal/store`: descarga, lectura y búsqueda.
- `internal/mcpserver`: `server.go` registra las herramientas (nombre,
  descripción y manejador), `tools.go` tiene sus tipos y manejadores, y
  `http.go` la seguridad HTTP.
- `internal/selfupdate` y `internal/userpaths`: actualización firmada y rutas.

Para añadir una herramienta MCP: tipos de entrada y salida y un método en
`tools.go`, y una línea en `New` (`server.go`).

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
  `aulaglobal-update.pub` y publica una versión con ese cambio. Actualiza también
  la clave embebida en install.sh (`SIGN_KEY`) e install.ps1 (`$SignKey`): una
  prueba de Go falla si los tres archivos no coinciden.
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

## Instaladores (install.sh e install.ps1)

Dos scripts en la raíz del repositorio, publicados como activos de cada versión
(`release.extra_files` en .goreleaser.yaml), de modo que las URL `latest/download`
siempre apuntan a los de la última versión:

```sh
# macOS y Linux (sh POSIX: dash, busybox ash, /bin/sh de macOS)
curl -fsSL https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest/download/install.sh | sh
```

```powershell
# Windows PowerShell 5.1 o PowerShell 7
irm https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest/download/install.ps1 | iex
```

Flujo (el mismo en ambos; salida en español con los marcadores ✓ ! ✗ · de setup y
pasos «[1/5]…»):

1. Detecta sistema y arquitectura (`uname`; en Windows `RuntimeInformation`, con
   `PROCESSOR_ARCHITEW6432`/`PROCESSOR_ARCHITECTURE` como respaldo) y la traduce a la
   etiqueta del archivo: `macos_apple-silicon`, `macos_intel`, `linux_x64`,
   `linux_arm64`, `windows_x64`, `windows_arm64`. Una terminal bajo Rosetta se trata
   como Apple Silicon. Cualquier otra combinación (incluido Windows de 32 bits) acaba
   con un error que apunta a la instalación manual.
2. Resuelve la versión: la última (`/releases/latest` redirige a
   `/releases/tag/vX.Y.Z`; con `wget` se lee el nombre de archivo de
   `latest/download/checksums.txt`) o la fijada con `AULAGLOBAL_VERSION`. La etiqueta
   se valida con semver estricto `vX.Y.Z` (sin prerelease).
3. Pregunta la carpeta (`~/.local/bin` o `%LOCALAPPDATA%\Programs\aulaglobal-mcp`),
   informa de la versión ya instalada, si la hay, y de que se sustituirá (o de que ya
   tiene la misma y ofrece reinstalar).
4. Descarga el archivo, `checksums.txt` y `checksums.txt.sig` a una carpeta temporal
   que se borra siempre al terminar.
5. **Verifica la firma antes que nada** con `ssh-keygen -Y verify` (namespace
   `aulaglobal-mcp-update`, allowed_signers `aulaglobal-mcp-update <clave>` generado en
   la carpeta temporal) y solo después comprueba el SHA-256 del archivo contra la línea
   exacta de `checksums.txt`. No hay ninguna opción para saltarse la firma; si falta
   `ssh-keygen` o es demasiado antiguo (sin `-Y verify`: OpenSSH < 8.1) el instalador
   aborta con un mensaje en español y el enlace de instalación manual. En Windows se
   indica cómo activar el «Cliente OpenSSH» (Configuración → Aplicaciones →
   Características opcionales); `ssh-keygen` se busca en el PATH y en
   `%WINDIR%\System32\OpenSSH`. En PowerShell el contenido de `checksums.txt` se
   escribe byte a byte en la entrada estándar del proceso (sin redirecciones de
   PowerShell, que en 5.1 recodifican).
6. Extrae solo la entrada raíz `aulaglobal-mcp` / `aulaglobal-mcp.exe` por nombre
   exacto y la instala de forma atómica: copia a un temporal en la misma carpeta y
   `mv` (Windows: el `.exe` en uso se renombra a `.exe.old`, como hace `update`, y se
   restaura si falla; si no se puede apartar se pide cerrar el asistente).
7. PATH (si la carpeta no está ya): pregunta [S/n] y añade una línea idempotente
   marcada con `# aulaglobal-mcp` al perfil: `~/.zshrc` con zsh; `~/.bashrc` con bash (y
   `~/.bash_profile` en macOS); `~/.config/fish/config.fish` con fish, usando
   `set -gx PATH …`; `~/.profile` con cualquier otro. En Windows, `SetEnvironmentVariable('Path',
   …, 'User')` de forma idempotente (sin distinguir mayúsculas ni barra final) y
   también `$env:Path` de la sesión.
8. Ejecuta `<carpeta>/aulaglobal-mcp setup` con la entrada conectada a `/dev/tty`
   (Windows: la consola). Sin terminal no lo ejecuta y escribe el comando.

Con `curl | sh` la entrada estándar es el propio script, por eso las respuestas se
leen de `/dev/tty`; sin terminal (CI, cron) se usan los valores predeterminados y se
avisa. Todo el script está dentro de `main()`, llamado en la última línea: una
descarga cortada no ejecuta nada. No se llama a `xattr`: `curl` no marca la descarga
en cuarentena. En Windows `Invoke-WebRequest` tampoco añade Mark-of-the-Web, por lo
que no hace falta `Unblock-File`.

### Variables de entorno

| Variable | Efecto |
| --- | --- |
| `AULAGLOBAL_VERSION` | Instala esa versión (`v0.1.5` o `0.1.5`) en lugar de la última. Con `curl \| sh`: `curl … \| AULAGLOBAL_VERSION=v0.1.5 sh`; en PowerShell: `$env:AULAGLOBAL_VERSION='v0.1.5'` antes de `irm … \| iex`. |
| `AULAGLOBAL_INSTALL_DIR` | Carpeta de instalación; no se pregunta. Admite `~` y espacios; se rechazan `"`, `$`, `` ` ``, `\` y `:` en POSIX por la línea que se escribe en el perfil. |
| `AULAGLOBAL_YES=1` | Acepta todas las opciones predeterminadas sin preguntar (también el PATH). Si hay terminal, `setup` se ejecuta igualmente, porque es interactivo por naturaleza. |
| `AULAGLOBAL_RELEASE_BASE_URL` | **Solo para pruebas.** Sustituye `https://github.com/Alejandro12120/aulaglobal-mcp/releases` (de ahí salen `latest` y `download/<tag>/…`) y desactiva las restricciones https de `curl`. No cambia la clave de confianza: la verificación de firma sigue usando la clave embebida. |

La clave pública está embebida en cada script (`SIGN_KEY` en install.sh,
`$SignKey` en install.ps1). Una prueba de Go (`internal/selfupdate/installers_test.go`,
que corre en toda la matriz de CI) falla si alguna difiere de
`internal/selfupdate/update_key.pub`, si cambian las etiquetas de archivo respecto a
`AssetName`/.goreleaser.yaml, si install.ps1 deja de ser ASCII (Windows PowerShell 5.1
puede decodificar mal `irm | iex` el texto no ASCII; los acentos se escriben como
`\uXXXX` y los convierte la función `T`) o si install.sh deja de terminar en
`main "$@"`. Al rotar la clave hay que actualizar los tres archivos a la vez.

### Publicación y CI

- .goreleaser.yaml: `release.extra_files` publica los dos scripts y
  `checksum.extra_files` los incluye en `checksums.txt`, así que también quedan
  cubiertos por `checksums.txt.sig` (verificables a mano con `ssh-keygen -Y verify`).
  Los instaladores no se fían de esa línea para sí mismos; es una comodidad para quien
  quiera auditarlos. `update` ignora las líneas que no son su archivo.
- ci.yml y release.yml: `shellcheck -s sh install.sh` (y los scripts de prueba) en
  Linux; en Windows, `scripts/lint-install-ps1.ps1` con el analizador de Windows
  PowerShell 5.1 y, con `-Analyze`, PSScriptAnalyzer 1.24.0 (se instala del
  repositorio PSGallery, unos segundos; `Write-Host` está excluido a propósito).
- El arnés de extremo a extremo (siguiente apartado) no corre en CI: necesita Docker
  e imágenes grandes y se ejecuta a mano al tocar los instaladores.

### Pruebas de los instaladores

```sh
scripts/test-install.sh        # install.sh (dash, bash, busybox ash) e install.ps1 (pwsh en Linux)
scripts/test-install.sh sh     # solo install.sh
scripts/test-install.sh ps     # solo install.ps1
```

Necesita Docker. Imágenes: `golang:1.26.9` (trae OpenSSH, curl y wget; dentro del
contenedor se instala `busybox-static`) y `mcr.microsoft.com/powershell:ubuntu-24.04`
(sobrescribibles con `GO_IMAGE`/`PS_IMAGE`; `GOCACHE_VOLUME` aporta una caché de Go).
El repositorio se monta de solo lectura y el único estado es un volumen temporal que
se borra al terminar. Los contenedores se ejecutan sin terminal (`-t` no se usa): el
arnés lo comprueba.

Qué hace (scripts/test-install/): compila el binario real con `-X main.version`,
`mkrelease.go` empaqueta versiones falsas con los nombres de GoReleaser (tar.gz y
zip, `checksums.txt`) y las firma con una clave ed25519 desechable generada en la
prueba, y `server.go` las sirve en loopback imitando `/releases/latest` (redirección),
`/releases/latest/download/…` y `/releases/download/<tag>/…`. Para que el script
publicado no tenga ningún mecanismo de cambio de clave, las pruebas ejecutan una
**copia** con la línea `SIGN_KEY` sustituida (se comprueba que difiere solo en esa
línea); el script original, contra la misma versión, debe fallar por la firma.

install.sh se ejecuta como `cat install.sh | env -i … <shell>` (stdin es el script,
como con `curl | sh`) bajo dash, bash y busybox ash: instalación con
`AULAGLOBAL_YES`/`AULAGLOBAL_INSTALL_DIR`, versión por defecto y fijada, segunda
ejecución (misma versión, idempotencia del PATH), actualización, perfiles
(zsh/bash/fish/otro, macOS simulado con un `uname` falso), archivo manipulado,
`checksums.txt` manipulado, firma ausente, clave equivocada, sin `ssh-keygen`,
`ssh-keygen` antiguo, arquitectura/SO no soportados, solo `wget`, carpeta imposible,
script truncado, y, con una terminal real creada con `script`, las preguntas de
carpeta/PATH, y que `setup` recibe un terminal en la entrada. Sin terminal se
comprueba que `setup` no se ejecuta (no contacta con UC3M). Bajo busybox ash no se
simulan `uname` ni la ausencia de `wget`, porque busybox ejecuta sus applets sin
mirar el PATH.

install.ps1 se ejecuta con PowerShell 7 en Linux como `texto | Invoke-Expression`:
el mismo flujo completo (arquitectura, versión por redirección, descarga,
firma con el `ssh-keygen` real, SHA-256, zip, instalación, `.old`, `.exe` «en uso»
simulado con un `.old` imborrable, errores) más pruebas unitarias de las funciones
puras extraídas con el AST (`ConvertTo-ReleaseTag`, `Get-ChecksumEntry`,
`Add-PathEntry`, `ConvertTo-QuotedArg`, `Confirm-Choice`…) y
`lint-install-ps1.ps1 -Analyze`.

**No está verificado en un Windows real**: Windows PowerShell 5.1 (solo se comprueba
en CI que el analizador lo acepta), la escritura del PATH de usuario en el registro,
`ssh-keygen.exe` de Windows (el inbox de algunas compilaciones es OpenSSH 7.x, sin
`-Y verify`: el instalador lo detecta y lo explica), el renombrado de un `.exe` en uso
y la barra de progreso/consola de verdad. Tampoco se ha probado el `/bin/sh` de
macOS (bash 3.2 en modo POSIX); el script evita bashismos y pasa ShellCheck con
`-s sh`.

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

release.yml ejecuta GoReleaser v2.8.2 y publica seis archivos con checksums.txt y checksums.txt.sig (ver Actualización automática), más install.sh e install.ps1 (ver Instaladores).
Compila con CGO_ENABLED=0, trimpath y main.version; --version imprime la versión.
No publica imágenes GHCR ni attestations por ahora. Los binarios aún no tienen
firma de sistema operativo (la firma de checksums.txt solo protege la actualización): quedan pendientes Authenticode, firma/notarización de Apple y provenance.
El proyecto usa licencia MIT (archivo LICENSE). Cada archivo de release y la imagen Docker incluyen también THIRD_PARTY_NOTICES.md (ver Avisos de terceros).



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

- https://antigravity.google/docs/mcp: ruta actual, esquema, interfaz y recarga CLI/App.
- https://antigravity.google/docs/getting-started: instalación oficial de `agy` y ubicaciones.
- https://antigravity.google/docs/changelog: notas vigentes de App, CLI e IDE.
- https://antigravity.google/cli/install.sh: instalador empleado en el contenedor.

El esquema multiplataforma y la interfaz gráfica están verificados documentalmente;
la generación de configuración y ayuda se verificaron también con el binario Linux.

## Registro unificado de asistentes

`assistant.go` define `assistant`: ID, Name, Detect, Inspect, Apply,
Manual y Reload (y, para `uninstall`, Find, Remove y ManualRemove). La única lista ordenada `assistants` sirve para detección,
selección por ID, ayuda y pruebas de contrato. Los estados son no instalado,
falta, idéntico, diferente y otro ámbito. Los errores de inspección se cierran
sin escribir ni mostrar diagnósticos privados.

`configureClients` inspecciona antes de preguntar, muestra el inventario y usa
siempre [S/n] para añadir o actualizar. Con dos o más cambios propone configurar
todos; `e` permite elegir y `n` omite todos los pendientes. Las entradas idénticas
no preguntan. El resumen distingue añadido, actualizado, ya estaba, omitido y
falló, y solo imprime Reload de los clientes modificados. Las transcripciones en
`cmd/aulaglobal-mcp/testdata/setup-*.golden` fijan el formato.

Para añadir un asistente, sigue **3 pasos**:

1. Crea `assistant_<id>.go` con un tipo que implemente `assistant` (o construye
   un `fileAssistant` con las particularidades del formato, ruta, detección y
   recarga). Ahí va todo lo propio del asistente, también quitar su entrada.
2. Añade una línea a `assistants`. Su ID queda disponible en --help y --clients.
3. Ejecuta las pruebas compartidas `TestAssistantContracts` (`assistant_test.go`) y añade fixtures o
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

## Desinstalación (`aulaglobal-mcp uninstall`)

Implementada en cmd/aulaglobal-mcp: `uninstall.go` (opciones, menú y
orquestación), `uninstall_plan.go` (qué se hará en cada opción),
`uninstall_path.go` (perfiles de shell y PATH de Windows), `uninstall_data.go`
(token y carpeta de apuntes), `uninstall_program.go` (borrar el ejecutable),
`uninstall_windows.go` / `uninstall_other.go` (registro y proceso auxiliar de
Windows). Quitar la entrada de cada asistente está en su `assistant_<id>.go`. Texto en español y
mismo estilo visual que setup (✓ ! ✗ ·, `[s/N]`).

### Opciones y flags

Sin flags y con terminal abre el menú: 1 desconectar asistentes (predeterminada),
2 cerrar la sesión (borrar la clave de acceso), 3 desinstalar todo. En todos los
casos se imprime el **plan** exacto y se pide `¿Continuar? [s/N]` (Enter = no);
después se imprime el resultado con ✓/·/!/✗ por elemento. Cualquier ✗ da salida
distinta de 0.

| Flag | Efecto |
|---|---|
| `--clients ids` | opción 1 para esos asistentes (IDs de setup), sin menú |
| `--logout` | opción 2 |
| `--all` | opción 3 (conserva la carpeta de apuntes) |
| `--delete-data` | con `--all`, permite borrar la carpeta de apuntes (reglas abajo) |
| `--yes` | omite las confirmaciones; **no** implica `--delete-data` ni quita entradas ajenas |
| `--dry-run` | imprime el plan y no cambia nada |
| `--secrets-dir`, `--data-dir` | rutas personalizadas, como en setup (`AULAGLOBAL_TOKEN_FILE` y `AULAGLOBAL_DOWNLOAD_DIR` también se respetan) |
| `--help` | uso, salida 0 |

Combinaciones inválidas (mensaje claro, sin cambios): más de una de `--clients`,
`--logout`, `--all`; `--delete-data` sin `--all`; `--clients` vacío o con ID
desconocido; argumentos sobrantes. Sin terminal y sin flags se imprime la ayuda y
sale con error; con flags pero sin terminal exige `--yes` (o `--dry-run`).

### Opción 1: desconectar asistentes

La interfaz `assistant` tiene tres métodos nuevos: `Find` (qué hay bajo
`aulaglobal`: comando y env), `Remove` (quita solo esa entrada) y `ManualRemove`
(instrucciones manuales). Solo se quitan entradas cuyo comando es **este**
ejecutable o un binario llamado `aulaglobal-mcp`; si la entrada difiere se
muestra y se pregunta (`[s/N]`). Con `--yes` o sin terminal las ajenas no se
tocan nunca (aviso `!`).

- Claude Code: `claude mcp remove --scope user aulaglobal` y comprobación
  posterior con `mcp get`. Los ámbitos local y proyecto se detectan y se
  explican (con el comando para quitar el local en su carpeta); **no** se tocan
  ni `.mcp.json` ni el ámbito local. Limitación: `mcp get` solo muestra el
  ámbito de mayor prioridad, de modo que una entrada de usuario tapada por una
  local no se ve desde esa carpeta. La salida del CLI nunca se imprime.
- Claude Desktop y Antigravity (`mcpServers`) y OpenCode (V2 `mcp.servers`, V1
  `mcp`): la entrada se corta **por bytes** (se localiza con el decodificador JSON
  y se elimina solo ese miembro y su coma), así que orden de claves, sangría y
  números grandes del resto del archivo quedan idénticos. JSON inválido, claves
  duplicadas, JSONC con comentarios y formatos ambiguos se rechazan.
- Codex: se elimina línea a línea la tabla `[mcp_servers.aulaglobal]` y sus
  subtablas; se rechazan tablas en línea, claves con puntos y cadenas multilínea.
- Los archivos se escriben con el mismo `atomicConfig` que setup (temporal +
  rename, original en `.bak`); archivos no regulares (enlaces) se rechazan.
- Si algo no se puede cambiar con seguridad: ✗ con la ruta y los pasos manuales,
  sin tocar el archivo.

### Opción 2: cerrar sesión

Borra el token y sus acompañantes (`.private`, `.meta.json`, temporales
`.token-*`) y su carpeta si queda vacía. La ruta sale de `--secrets-dir`, luego
`AULAGLOBAL_TOKEN_FILE`, luego el valor por defecto; además se borran las rutas
`AULAGLOBAL_TOKEN_FILE` que figuren en entradas de asistentes propias, solo si
son archivos regulares de hasta 64 KiB. Programa y asistentes no cambian; se
indica que `aulaglobal-mcp setup` reconecta.

### Opción 3: desinstalar todo

Por este orden: asistentes (opción 1 para todos), token y carpeta de
configuración `<UserConfigDir>/aulaglobal-mcp`, caché de avisos
`<UserCacheDir>/aulaglobal-mcp`, PATH, carpeta de apuntes (si procede) y por
último el programa. Las carpetas propias solo se borran con `RemoveAll` si se
llaman exactamente `aulaglobal-mcp`.

| Sistema | PATH | Programa |
|---|---|---|
| Linux / macOS | Se buscan, en todos los perfiles que escribe install.sh (`~/.zshrc` o `$ZDOTDIR/.zshrc`, `~/.bashrc`, `~/.bash_profile`, `$XDG_CONFIG_HOME/fish/config.fish`, `~/.profile`), las líneas con la forma exacta del instalador y terminadas en `# aulaglobal-mcp`. Solo se quita la que añade la carpeta de **este** ejecutable (las de otra carpeta se dejan y se avisa). Copia en `<perfil>.bak`; se conservan permisos y enlaces simbólicos; idempotente | `os.Remove` del ejecutable (resuelto con EvalSymlinks); su carpeta solo si se llama `aulaglobal-mcp` y está vacía. La carpeta típica `~/.local/bin` nunca se borra |
| Windows | Se quita solo la entrada igual (expandida, sin barra final, sin distinguir mayúsculas, como install.ps1) del valor `Path` de `HKCU\Environment` con `golang.org/x/sys/windows/registry` (ahora dependencia directa; ya estaba enlazada, THIRD_PARTY_NOTICES.md no cambia), conservando el tipo REG_SZ/REG_EXPAND_SZ y el resto del valor; después `WM_SETTINGCHANGE`. No se guarda copia del valor | Un ejecutable en uso no se puede borrar: `selfupdate.MoveAside` lo renombra a `<exe>.old` y se lanza `cmd.exe /d /s /c "ping -n 3 127.0.0.1 >nul & del /f /q "<exe>.old" & rmdir "<carpeta>""` sin ventana (`CREATE_NO_WINDOW`, grupo de procesos nuevo) que sobrevive a este proceso; `rmdir` solo vacía y solo si la carpeta se llama `aulaglobal-mcp`. Rutas con `% & ^ \| < > " !` no se programan. Si falla, se dice el archivo exacto que borrar (la limpieza de `.old` al arrancar de selfupdate sigue de respaldo si se reinstala) |

En un contenedor (`selfupdate.InContainer`) no se borran el binario ni el PATH
(se explica); el resto sí.

### Carpeta de apuntes (solo opción 3)

Candidatas: el `AULAGLOBAL_DOWNLOAD_DIR` de cada entrada propia (o la
predeterminada `~/AulaGlobal` si no lo tiene), `--data-dir` /
`AULAGLOBAL_DOWNLOAD_DIR` del entorno, o la predeterminada si no hay ninguna.
Si difieren se pregunta por cada una:
`¿Borrar también la carpeta AulaGlobal con tus materiales (<ruta>)? [s/N]`
(Enter = conservar). Con `--yes` o sin terminal se conserva salvo `--delete-data`.
Nunca se borra fuera de esa carpeta. **Se rechaza** (aviso y se deja a mano) si:
la ruta está vacía o no es absoluta, es la raíz, es la carpeta personal o la
contiene, contiene el programa/configuración/caché/token, es un enlace simbólico
o no es una carpeta, se llama como una carpeta personal del sistema (Documents,
Descargas, Dropbox...), o no contiene el directorio marca `.aulaglobal-cache` (una
carpeta vacía sí se puede quitar). Una carpeta que no es la predeterminada solo se
borra respondiendo `s` en una terminal, con un aviso extra (`--delete-data` no
basta). La comprobación se repite justo antes de borrar.

### Pruebas

`uninstall_*_test.go`: plantilla de instalación falsa en directorio temporal
(HOME, config, caché, perfiles, ejecutable, asistentes y registro inyectados; nunca
se tocan los archivos, PATH, registro ni ejecutable reales y no se contacta con
AulaGlobal), pruebas por asistente (incluido OpenCode V1/V2, TOML con otros
servidores, JSON/TOML malformados, enlaces), Claude Code con un ejecutable falso
real (el propio binario de pruebas reutilizado vía `TestMain`, `claude`/`claude.exe`)
y con un ejecutor simulado, lógica de PATH de Windows con un almacén simulado,
extremo a extremo de la opción 3, `--dry-run` comprobado con hash del
directorio, y transcripciones doradas (`testdata/uninstall-*.golden`,
`UPDATE_GOLDEN=1` las regenera). El código específico de Windows se compila en los
seis destinos; su parte pura se prueba en todos los sistemas, pero el registro y el
proceso auxiliar solo se han podido verificar compilando.
