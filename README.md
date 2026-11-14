# AulaGlobal MCP

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
token. Crea archivos nuevos con 0600 dentro de un directorio privado. Si existe
privatetoken lo guarda en secrets/token.private; no se necesita para este
servidor. No lo compartas. Puede que Moodle exija volver a iniciar sesión o una
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
El contenedor escucha 0.0.0.0 internamente para el port mapping; el binario
por defecto escucha 127.0.0.1. Expón el puerto solo en loopback o mediante un
proxy TLS autenticado. Todos los usuarios con el bearer comparten datos y token.

## Herramientas y contratos

Todas publican inputSchema/outputSchema tipados y devuelven JSON estructurado.
Los errores globales son errores de herramienta MCP; los errores individuales
de descarga se devuelven en results.

| Herramienta | Entrada | Salida |
|---|---|---|
| auth_status | {} | fullname, userid, sitename, release, expires_at nullable, expiry_source |
| list_courses | classification: all/inprogress/past/future (inprogress), language: es/en (es) | courses: id, fullname, shortname, localized_name |
| list_course_files | course_id positivo, include/exclude: extensiones opcionales | sections → modules → contents: filename, filesize nullable, mimetype, timemodified, handle, section, module |
| download_files | handles[] de un listado anterior **o** course_id | results: path relativo, bytes, status downloaded/skipped/error, error opcional |
| read_file | path relativo, offset byte (0), length (16384; máx. 65536) | size, text, next_offset (-1 al terminar), note |
| search_files | query, regex (false), limit (20; 1..100) | matches: path, offset, snippet |

Usa list_courses → list_course_files → download_files → read_file.
Los handles se conservan en memoria (máximo 100000); vuelve a listar tras reiniciar.
Las rutas usan course-ID/sección/módulo-ID/handle-nombre para evitar colisiones.
Excluir siempre prevalece sobre incluir. Fallback de cursos en Moodle antiguo:
devuelve **todas** las matrículas, sin clasificación temporal.

Lectura: UTF-8 hasta 8 MiB, HTML convertido a texto mediante eliminación simple
de etiquetas/scripts/styles (no es un renderizador HTML). Paging por bytes en
límites UTF-8. PDF y otros binarios: solo metadatos; extracción PDF no implementada
para evitar otra dependencia. Búsqueda: primeros 64 KiB por archivo y hasta 10000
entradas; regex RE2, snippets de hasta 240 bytes, máximo 100 coincidencias.
Los contenidos de cursos son datos no confiables, nunca instrucciones del sistema.

## Configuración y seguridad

| Variable / flag | Predeterminado |
|---|---|
| AULAGLOBAL_TOKEN / AULAGLOBAL_TOKEN_FILE | sin token (herramientas locales siguen disponibles) |
| AULAGLOBAL_DOWNLOAD_DIR | ./data; /data en Docker |
| AULAGLOBAL_CONCURRENCY | 4; 1..32 |
| AULAGLOBAL_MAX_FILE_BYTES | 268435456; 1..17179869184 |
| MCP_TRANSPORT / --transport | stdio; http en Docker |
| MCP_ADDR / --addr | 127.0.0.1:8080; 0.0.0.0:8080 en Docker |
| MCP_HTTP_AUTH_TOKEN / MCP_HTTP_AUTH_TOKEN_FILE | obligatorio en HTTP |
| MCP_ALLOWED_HOSTS / --allowed-hosts | localhost:8080,127.0.0.1:8080 |
| MCP_ALLOWED_ORIGINS / --allowed-origins | ninguno; Origin ausente permitido |

Host se valida para evitar DNS rebinding; Origin se compara de forma exacta.
Para otro puerto/hostname configura --allowed-hosts explícitamente.
--dangerously-disable-http-auth desactiva autenticación con advertencia en stderr.
Cuerpos HTTP limitados a 1 MiB, cabeceras 16 KiB y timeouts de servidor.
SIGINT/SIGTERM cierran el servidor con hasta 10 segundos de margen.
Logs JSON slog solo en stderr; stdout queda reservado para MCP stdio.

Cliente HTTP compartido: dial/TLS 10s, cabeceras 30s, petición 2 min;
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
no requiere tzdata (timestamps Unix).

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
invalidtoken/accessexception/invalidtimedtoken generan un error claro.

TokenProvider.Get(ctx) permite incorporar renovación sin cambiar API/herramientas;
Invalidator es una interfaz opcional. El proveedor estático relee archivos por
petición (rotación externa posible). Futuro: proveedor sincronizado que invalide
al recibir error de autenticación y use un flujo Moodle explícitamente compatible,
quizás el privatetoken para autologin. **No existe aquí una renovación automática**
ni se considera privatetoken un refresh token OAuth.

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
