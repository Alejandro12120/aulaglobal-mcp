# AulaGlobal MCP · Referencia técnica

Servidor en Go que consulta las asignaturas de AulaGlobal (UC3M) y descarga y lee
sus documentos. Usa el SDK oficial de MCP para Go y funciona por stdio o por
Streamable HTTP en /mcp. La versión de Go está fijada en 1.26.9, también en el
contenedor de compilación, así que no hace falta Go en el host.

## Token

Las herramientas MCP no aceptan credenciales. Configura solo una de
AULAGLOBAL_TOKEN o AULAGLOBAL_TOKEN_FILE; si están las dos, gana el archivo.
Mantén los archivos de secretos con permisos 0600 y sus directorios con 0700.

Para convertir una cookie MoodleSessionag de tu propia sesión, copia solo su
valor desde las herramientas del navegador. Este ejemplo en Bash usa el binario
compilado y no deja el valor en pantalla ni en el historial:

```bash
IFS= read -rs -p 'MoodleSessionag: ' cookie
printf '\n' >&2
printf '%s' "$cookie" | ./aulaglobal-mcp token-from-cookie --output secrets/token
unset cookie
```

El subcomando lee la cookie de stdin, nunca de argv, y rechaza un terminal como
entrada para que no haya eco. No sigue redirecciones: exige una cabecera Location
`moodlemobile://token=...`, decodifica el base64 y verifica
MD5(siteurl+passport) con un passport aleatorio. Nunca imprime el token.

Crea los archivos nuevos con permisos 0600. Con `--force` los sustituye de forma
atómica dentro de un directorio privado. Si Moodle devuelve un privatetoken, lo
guarda en secrets/token.private, aunque este servidor no lo necesita. También
guarda `<tokenfile>.meta.json` (0600), que solo contiene `obtained_at` en
RFC3339. `auth_status` lee ese archivo junto a AULAGLOBAL_TOKEN_FILE y devuelve
`token_obtained_at` y `token_age_days`. No compartas estos archivos.

Si Moodle exige volver a iniciar sesión o una confirmación, el subcomando
devuelve un error. No automatiza el SSO.

Para compilar el binario sin instalar Go:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.26.9   sh -c 'CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o aulaglobal-mcp ./cmd/aulaglobal-mcp'
```

## Stdio

```bash
AULAGLOBAL_TOKEN_FILE="$PWD/secrets/token" ./aulaglobal-mcp --transport stdio
```

Configuración para Claude Code, con el formato habitual de los clientes MCP.
Sustituye las rutas absolutas:

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

El contenedor se ejecuta con el UID 65532, que debe poder leer los secretos. Con
archivos 0600, haz que su propietario sea 65532 o ejecuta el contenedor con tu UID
y un directorio de datos en el que pueda escribir.

## HTTP con Docker

```bash
docker build -t aulaglobal-mcp:local .
# MCP_HTTP_AUTH_TOKEN debe contener un secreto aleatorio independiente del token Moodle.
docker run --rm --name aulaglobal -p 127.0.0.1:8080:8080   -e MCP_HTTP_AUTH_TOKEN   -e AULAGLOBAL_TOKEN_FILE=/run/secrets/token   -v /ruta/secrets/token:/run/secrets/token:ro   -v ag-data:/data aulaglobal-mcp:local
```

Configura el cliente HTTP con la URL http://127.0.0.1:8080/mcp y la cabecera
`Authorization: Bearer <secreto MCP>`. El transporte no guarda estado ni mantiene
sesiones. Si mapeas otro puerto, la cabecera Host lleva el puerto del host, no el
del contenedor:
```bash
docker run --rm -p 127.0.0.1:9000:8080 -e MCP_ALLOWED_HOSTS=127.0.0.1:9000,localhost:9000 -e MCP_HTTP_AUTH_TOKEN -e AULAGLOBAL_TOKEN_FILE=/run/secrets/token -v /ruta/secrets/token:/run/secrets/token:ro -v ag-data:/data aulaglobal-mcp:local
```
Configura el cliente con http://127.0.0.1:9000/mcp. MCP_ALLOWED_HOSTS es una
lista de valores Host exactos separados por comas; no uses comodines.
Sin esa variable, el servidor acepta localhost y 127.0.0.1 con el puerto de
escucha.

Dentro del contenedor el servidor escucha en 0.0.0.0 para que funcione el mapeo
de puertos. Fuera de Docker, el binario escucha por defecto en 127.0.0.1. Expón el
puerto solo en loopback o detrás de un proxy TLS con autenticación. Todos los
usuarios que tengan el bearer comparten los datos y el token de AulaGlobal.

## Herramientas y contratos

Todas las herramientas publican inputSchema y, salvo view_image, outputSchema
tipados, y devuelven JSON estructurado. Un error que afecta a toda la llamada se
devuelve como error de herramienta MCP. Los errores de descargas sueltas van dentro de `results`. El
contenido de los cursos son datos no confiables, nunca instrucciones del sistema.

| Herramienta | Entrada | Salida |
|---|---|---|
| auth_status | {} | token_valid, sitename, release, expires_at nullable, expiry_source, token_obtained_at/token_age_days opcionales, update_available opcional. No devuelve nombre ni ID de usuario (ver Privacidad y minimización de datos) |
| list_courses | classification: all/inprogress/past/future (inprogress), language: es/en (es) | courses: id, fullname, shortname, localized_name |
| list_course_files | course_id positivo, include/exclude: extensiones opcionales | sections con modules y contents anidados: filename, filesize nullable, mimetype, timemodified, handle, section, module |
| download_files | handles[] de un listado anterior, o course_id | results: path relativo, bytes, status downloaded/skipped/error, error opcional |
| read_file | path relativo, offset byte (0), length (16384; máx. 65536) | size, text, next_offset (-1 al terminar), note |
| search_files | query, regex (false), limit (20; 1..100), path_prefix opcional | matches: path, page (si se conoce), offset dentro de página, snippet |
| list_assignments | course_ids opcional (cursos inprogress), status upcoming/overdue/all, include_submission_status (false), language es/en | assignments: fechas Unix + RFC3339 Europe/Madrid, estado de apertura, vista previa de intro (2000 runas), adjuntos con handles, estado de entrega opcional |
| get_assignment | assignment_id positivo (el `id` de list_assignments), course_id positivo opcional | intro completo, activity si Moodle la ofrece, fechas Unix + RFC3339 Europe/Madrid, estado de apertura, introattachments y activityattachments con handles. Texto y adjuntos no confiables |
| get_assignment_feedback | assignment_id positivo (el `id` de list_assignments, no el cmid), course_id positivo opcional | available y note (sin feedback visible no es un error), grade (omitido si no hay nota), grade_for_display, graded_date, comments: plugin, name, text en texto plano con marcadores `[image N: archivo]`, files con handle, filename, mimetype, filesize, plugin, area e image_index, previous_attempts (attempt_number desde 1, grade, comments, files), content_note. Contenido no confiable que puede incluir datos de terceros. Moodle registra un evento feedback_viewed |
| view_image | path relativo de download_files | Sin outputSchema ni structuredContent: un bloque de texto (path, mime, bytes, contenido no confiable) y un bloque de imagen. Solo png, jpeg, gif y webp según el contenido, máx. 3,75 MiB |

El orden habitual es list_courses, list_course_files, download_files y read_file.
Para las correcciones es list_assignments, get_assignment_feedback,
download_files y view_image.

El servidor guarda los handles en memoria, con un máximo de 100000. Tras reiniciar
hay que volver a listar, porque la descarga por handle necesita la URL del
listado.

Las rutas siguen el patrón `<Asignatura localizada>/<Sección>/<archivo>` y
conservan las subcarpetas (filepath) de los módulos de tipo carpeta. Todos los
nombres pasan por la sanitización. Solo una colisión añade un sufijo legible
(2), (3), etc., que se asigna en orden de handle y queda guardado en el
manifiesto.

Cada curso tiene un nombre local canónico en español, que el manifiesto guarda
por ID aunque el curso cambie de nombre. El idioma de visualización no cambia la
carpeta. El servidor reutiliza en memoria los nombres que ya conoce, sin otra
consulta de curso.

El manifiesto `.aulaglobal-cache/manifest.json` se escribe de forma atómica.
Registra para cada archivo un handle estable (hash de curso, módulo, filepath y
filename), la ruta, el tamaño y timemodified. Si cambia la revisión de la URL, el
handle es el mismo y no se crea otra copia. El skip funciona tras reiniciar, y
las rutas ya asignadas se mantienen aunque cambie el nombre del curso. Las
descargas del diseño antiguo no se migran.

En los filtros, exclude siempre gana a include. En un Moodle antiguo, el respaldo
de list_courses devuelve todas las matrículas, sin clasificación temporal.

### Lectura y extracción

read_file y search_files leen texto UTF-8, HTML (convertido a texto con una
limpieza simple), PDF, docx, pptx, xlsx, xlsm, odt, odp y ods. El servidor no hace
OCR ni ejecuta macros.

Para PDF usa ledongthuc/pdf, una biblioteca en Go puro con licencia BSD-3-Clause,
sin dependencias transitivas y con la versión fijada en go.mod
([fuente](https://github.com/ledongthuc/pdf)). El texto de cada página va
precedido de un marcador `[Page N]`. Para ZIP y XML solo usa la biblioteca
estándar. El XML lo lee `internal/store/xmlscan.go`, un tokenizador propio que no
reserva memoria por token. Existe porque `encoding/xml` era el mayor coste: con
él, un docx tardaba unas 5 veces más.

Los formatos de oficina se convierten a Markdown (GFM). Todos los parsers
escriben, en una sola pasada y sin árbol DOM, en el mismo serializador
(`markdown.go`). Solo la tabla en curso queda en búfer, porque GFM necesita el
número de columnas antes de escribir la cabecera. El escapado de `|` y `\`, el
paso de los saltos de línea de una celda a `<br>` y el resto de la normalización
están solo en ese archivo. `[Page N]`, `Page{Number, Text}` y `Match.Page` no
cambian. Lo que cambia es que el texto de cada página es Markdown.

- docx. Los títulos salen del estilo del párrafo. Los ids de estilo están
  localizados (en español, `Ttulo1`), así que el parser los resuelve con
  `word/styles.xml` mediante `w:name` ("heading N" o "Title"), `w:outlineLvl` y
  `w:basedOn`. Las listas salen de `w:numPr`, numeradas o con viñetas según
  `word/numbering.xml`. Una celda combinada con `w:gridSpan` produce celdas
  vacías, y una tabla anidada se aplana dentro de su celda. El parser ignora el
  texto de revisiones borradas (`w:del`) y el de los campos (`w:instrText`). Solo
  lee los estilos y la numeración cuando un párrafo los necesita. No hay
  negrita, cursiva ni notas al pie.
- pptx. El orden y el número de página salen de `ppt/presentation.xml`
  (`sldIdLst`) y sus relaciones, no del nombre del archivo, así que slide10 va
  después de slide9. Si faltan esos archivos o están rotos, el parser ordena por
  el número del nombre y usa ese número como página. El título (`title` o
  `ctrTitle`) sale como `## `, las viñetas siguen `lvl`, `buChar` y `buAutoNum`, y
  las tablas `a:tbl` salen como tablas GFM. Las notas del orador (relación
  `notesSlide`) van detrás de la diapositiva, en la misma página, en un bloque
  `**Notes:**`. Los marcadores `sldNum`, `sldImg`, `hdr`, `ftr` y `dt` se omiten.
- odt. Lee `text:h`, `text:p`, listas anidadas y tablas, en las que cada
  `covered-table-cell` cuenta como celda vacía. `text:s`, `text:tab` y
  `text:line-break` pasan a espacios y saltos de línea. Omite comentarios, notas
  al pie y cambios registrados.
- odp. Cada `draw:page` es una página. Los marcos con
  `presentation:class="title"` salen como `## ` y `presentation:notes` como
  bloque `**Notes:**`.
- xlsx, xlsm y ods. Cada hoja es una página, en el orden del libro
  (`xl/workbook.xml` y sus relaciones; el número de página es la posición de la
  hoja). La página empieza con `## <nombre>` y sigue con una tabla GFM del rango
  usado. Las filas y columnas vacías del final se recortan.
  - En xlsx, el parser carga `xl/sharedStrings.xml` una sola vez, con un tope de
    8 MiB y 1 Mi cadenas. Coloca cada celda según su referencia `r="C5"` y
    rellena los huecos de columna. Los valores salen tal como están guardados,
    sin formato de número.
  - En ods, toma el texto mostrado de la celda o, si no lo hay, su valor
    guardado. Nunca expande `number-columns-repeated` ni `number-rows-repeated`
    más allá de la última celda o fila con contenido. Una fila admite como
    máximo 1024 columnas y una tabla 1 Mi celdas, y se conservan como máximo 8
    filas vacías seguidas.
- `.csv` se sigue leyendo como texto, sin convertir.

`decodeDocument` detecta el formato por los primeros bytes antes de mirar la
extensión. `%PDF-` indica PDF. `PK\x03\x04` indica ZIP, y entonces las entradas y
el `mimetype` de ODF deciden entre docx, pptx, xlsx, odt, odp y ods. La firma
OLE/CFB indica un `.doc`, `.ppt` o `.xls` antiguo, o un OOXML cifrado. Por eso un
`.docx` que en realidad es un PDF se lee como PDF. `supported()` sigue decidiendo
por la extensión, e incluye `.doc`, `.ppt` y `.xls` para que read_file devuelva
una nota clara.

Los errores tienen tipo y se comprueban con `errors.Is` (paquete `store`):

| Error | Casos |
|---|---|
| `ErrUnsupported` | Formato antiguo, ZIP que no es un documento conocido. |
| `ErrNeedsOCR` | "PDF has no text layer (scanned); OCR not supported". |
| `ErrEncrypted` | PDF con `/Encrypt`, documento de Office con contraseña. |
| `ErrMalformed` | "invalid PDF", "invalid ZIP document", "invalid document XML", "PDF text extraction failed". |
| `ErrLimit` | Los límites que se indican más abajo. |

Los mensajes anteriores no cambian y se añaden los nuevos. read_file los
devuelve en `note`.

La caché de extracción está en `.aulaglobal-cache`. Su clave combina la ruta, el
tamaño y el mtime en nanosegundos, con el prefijo `v2:` para no servir el texto
plano de versiones anteriores. Las escrituras son atómicas, y read_file y
search_files no pueden leer esa carpeta. Los fallos deterministas de la tabla
anterior también se guardan, como `code` y `error` en el mismo archivo, y se
devuelven sin volver a analizar el documento. Los vencimientos, las
cancelaciones, los errores de E/S y los pánicos nunca se guardan. Puedes borrar
la caché con el servidor parado para recuperar espacio; no hay limpieza
automática.

Límites de extracción:

- Entrada de 64 MiB y texto extraído de 8 MiB.
- PDF de 500 páginas. Un PDF más largo se rechaza para no presentar contenido
  incompleto.
- ZIP: 2048 entradas, 32 MiB por entrada, 64 MiB descomprimidos en total y una
  relación de compresión máxima de 200:1.

Cada extracción tiene un plazo de espera configurable, de 60 segundos por
defecto. Si vence, read_file devuelve "extraction in progress, retry shortly". El
parser sigue en uno de los dos slots globales y, al terminar, guarda en la caché
el resultado o el fallo determinista, aunque el cliente haya cancelado o agotado
su plazo. Un pánico del parser se recupera. Go no permite matar una goroutine,
así que un parser atascado retiene su slot hasta terminar. No hay aislamiento de
proceso ni límite duro de memoria frente a un PDF malicioso.

El parser de PDF añade 282624 bytes (276 KiB) al binario estático optimizado. La
imagen final ocupa 14326383 bytes (14.33 MB).

search_files recorre todo el texto extraído, con un máximo de 10000 entradas,
128 MiB de texto, 30 segundos y 100 resultados. `path_prefix` limita la búsqueda
a una asignatura o subcarpeta. Las expresiones regulares son RE2 y los
fragmentos miden hasta 240 bytes.

### Entregas

list_assignments ordena por fecha de vencimiento y deja al final las entregas sin
fecha. `upcoming` incluye las entregas con fecha futura y las que no tienen fecha
y están abiertas. `overdue` incluye las de fecha pasada, aunque ya estén
cerradas. `submissions_possible` se calcula con la apertura, el cutoff y
nosubmissions. Es una estimación por calendario y no tiene en cuenta permisos
individuales ni prórrogas. Ambas herramientas convierten el HTML con
`store.HTMLText`, que compacta espacios, conserva los saltos de párrafo y las
listas, y omite imágenes incrustadas sin descargarlas.

Si `intro` supera las 2000 runas, list_assignments indica que se puede abrir
el texto completo con get_assignment. Pasa el `id` como `assignment_id` y,
si lo conoces, el `course_id` para limitar la búsqueda a esa asignatura.
get_assignment consulta `mod_assign_get_assignments` y devuelve `intro` y
`activity` completos según lo que Moodle muestre al estudiante. Si la entrega
no aparece, devuelve «assignment not found».

Los handles de ambas herramientas sirven para download_files. Los adjuntos de
la introducción se guardan en `<Asignatura>/Entregas/<Entrega>/<archivo>` y los
de la actividad en `<Asignatura>/Entregas/<Entrega>/Actividad/<archivo>`.

### Correcciones e imágenes

get_assignment_feedback llama a `mod_assign_get_assignments` (de solo lectura y
con caché de 45 s; con `courseids[0]` si se da course_id, y si no, para todas las
matrículas) para obtener cmid, curso y nombre, y a
`mod_assign_get_submission_status` para la nota y los comentarios. Si la entrega no aparece, devuelve
«assignment not found». Si Moodle no devuelve
el objeto `feedback` (sin nota visible y plugins vacíos, flujo de calificación
sin liberar o nota oculta), `available` es false y `note` lo explica, sin error.
La nota -1 de Moodle (ASSIGN_GRADE_NOT_SET) significa sin calificar y se omite.

Los comentarios (HTML) se convierten en texto plano con `store.HTMLText`: las
etiquetas de bloque, `<br>` y los elementos de lista pasan a saltos de línea
(`- ` en las listas), se colapsan las líneas en blanco, se decodifican las
entidades y se descartan script y style. Cada `<img>` se sustituye en su sitio
por `[image N: archivo]` cuando su ruta (tras `pluginfile.php`, sin consulta y
decodificada) coincide con un archivo de `fileareas`. N empieza en 1 y es único
en toda la respuesta, intentos anteriores incluidos. Si no coincide, el marcador
es `[image: alt]` o `[external image]`, y la imagen nunca se descarga.

Los archivos salen de los `fileareas` de cada plugin, salvo editpdf, del que solo
se conserva el área `download` (el PDF anotado final). Se guardan en
`<Asignatura>/Entregas/<Entrega>/Feedback/<archivo>` y, para intentos
anteriores, en `.../Feedback/Intento <n>/<archivo>`. Sus handles se registran
en el mismo mapa que los de list_assignments y sirven para download_files, que
mantiene la comprobación de host de `Client.Download`. Se calculan con
`FeedbackHandle` (hash de curso, cmid, plugin, área y ruta de pluginfile, con un
prefijo propio), porque `StableHandle` colisionaría con el mismo nombre en dos
áreas o intentos. Si dos archivos distintos comparten carpeta y nombre, el
sufijo (2) de siempre los separa. `StableHandle` no cambia, así que los
manifiestos existentes siguen valiendo.

El `attempt_number` de los intentos anteriores empieza en 1 (Moodle cuenta desde
0) y es el que usa la carpeta `Intento <n>`.

La llamada hace que Moodle registre un evento `feedback_viewed`, visible para el
profesorado en los registros del curso. El contenido es no confiable y puede
incluir datos personales de terceros, por ejemplo el nombre de usuario de un
compañero en una revisión entre iguales.

view_image lee un archivo ya descargado con las mismas reglas de ruta que
read_file (relativa, dentro de la carpeta, sin `.aulaglobal-cache`), rechaza
enlaces simbólicos y archivos que no son regulares, y detecta el tipo con
`http.DetectContentType` sin fiarse de la extensión. Acepta image/png,
image/jpeg, image/gif e image/webp, hasta 3,75 MiB (así el base64 queda por
debajo de 5 MB, un límite habitual de los clientes). No declara outputSchema ni
structuredContent a propósito: algunos clientes prefieren el resultado
estructurado y ocultarían la imagen. Devuelve el texto con path, mime y bytes y
un `ImageContent`.

El estado de entrega opcional usa cuatro workers, hace como máximo 100 consultas
y devuelve los errores por entrega. Las fechas a cero salen como null en
RFC3339. tzdata va embebida para que las fechas funcionen en la imagen scratch.

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

El servidor valida la cabecera Host para evitar DNS rebinding y compara Origin de
forma exacta. Para otro puerto u otro hostname, configura `--allowed-hosts`.
`--dangerously-disable-http-auth` desactiva la autenticación y escribe una
advertencia en stderr.

Límites HTTP: cuerpo de 1 MiB, cabeceras de 16 KiB, ReadHeaderTimeout de 10 s,
ReadTimeout de 30 s e IdleTimeout de 60 s. Go 1.26 quita el deadline de lectura
cuando termina de leer el cuerpo y empieza la lectura de fondo, así que
ReadTimeout no limita la ejecución posterior (lo comprueba un test con una
conexión real). No hay WriteTimeout global, para permitir llamadas MCP largas. Si
el cliente se desconecta, su contexto se cancela. SIGINT y SIGTERM cierran el
servidor con hasta 10 segundos de margen. Los logs JSON de slog van solo a
stderr, porque stdout es el canal de MCP stdio.

Todas las peticiones a AulaGlobal usan un cliente HTTP compartido. Sus plazos son
10 s para la conexión y TLS, 30 s para las cabeceras y 60 s para las llamadas WS y
la conversión de la cookie. Las descargas no tienen plazo total, pero sí un
detector de inactividad. Cada petición recibe el contexto de la llamada, y las
redirecciones solo pueden ir al mismo host y esquema, con un máximo de 5. Las
llamadas WS van por POST de formulario, y el token solo viaja en la query para
pluginfile. Los errores de red se redactan y los mensajes arbitrarios de Moodle
no se propagan.

Las descargas son GET idempotentes y reintentan tras un timeout o un 5xx (3
intentos, con un backoff que se puede cancelar). Los POST de WS solo se repiten
tras un 429 o un 503, y solo si la función es de solo lectura (ver Acceso
respetuoso a AulaGlobal). Cada descarga comprueba el status y el tamaño, rechaza
HTML inesperado y errores JSON, y escribe en un temporal exclusivo que sincroniza
con fsync y después renombra. El skip compara tamaño y mtime.

os.Root confina las lecturas y escrituras, también ante enlaces simbólicos que
apunten fuera. Usa un volumen dedicado y no dejes que procesos no confiables
escriban en él. Los archivos se crean con 0600 y los directorios con 0700. El
servidor no guarda ninguna cookie.

La imagen scratch contiene solo el binario estático, los certificados CA, /data y
/licenses (LICENSE y THIRD_PARTY_NOTICES.md), y se ejecuta con el UID 65532. El
binario lleva tzdata embebida (Europe/Madrid con horario de verano).

## Acceso respetuoso a AulaGlobal

Todo el tráfico saliente hacia AulaGlobal (llamadas WS, descargas pluginfile y
launch.php) pasa por una única puerta por proceso
(`internal/aulaglobal/polite.go`). Complementa la concurrencia ya limitada
(AULAGLOBAL_CONCURRENCY) y no sustituye al respeto de las normas de uso de la
universidad.

**Limitador de ritmo.** Es un cubo de fichas (token bucket) que reserva el hueco
futuro, de modo que los hilos esperan en orden. Por defecto permite 4
peticiones/s con ráfaga de 8. Con eso, una consulta interactiva (cursos,
contenidos, tareas) no espera nunca, y una descarga masiva o la consulta del
estado de entregas se queda en 240 peticiones por minuto, del orden de lo que
genera un navegador. Se configura con `AULAGLOBAL_MAX_RPS`, un número decimal
entre 0.1 y 100. La ráfaga es el doble, con un mínimo de 1. Con un valor no
válido, el servidor no arranca. Un curso de 200 archivos tarda unos 50 s con el
valor por defecto. La espera respeta la cancelación del contexto. Las
redirecciones del mismo host (máximo 5) no consumen ficha propia.

**429 y 503.** El cliente lee `Retry-After`, en segundos o como fecha HTTP. Sin
esa cabecera espera 5 s y duplica la espera en cada intento, con ±25 % de
jitter. La espera nunca pasa de 120 s, y es de 1 s si el servidor indica 0. La
pausa es global. Cualquier goroutine puede fijarla y todas la respetan, también
las peticiones que ya estaban en cola. El cliente reintenta como máximo 2 veces
(3 intentos), y solo si la petición es de solo lectura. Son de solo lectura los
GET de descarga y de launch.php y las funciones WS de la lista cerrada
`readOnlyFns`. Todas las funciones que usa el cliente leen y ninguna escribe en
Moodle. Una función WS que no esté en esa lista nunca se reintenta, ni se
deduplica ni se cachea.

Si la pausa es mayor que el tiempo que le queda al llamante, o se agotan los
reintentos, la herramienta devuelve «AulaGlobal está limitando las peticiones;
vuelve a intentarlo en N s.» (`errors.Is(err, aulaglobal.ErrRateLimited)`). Una
descarga por lotes o la consulta del estado de entregas se detiene entera con ese
único error, en lugar de seguir enviando peticiones. Al repetirla, lo ya
descargado se omite. Los errores JSON con HTTP 200 (errorcode de Moodle) no son
límites de ritmo y no se reintentan.

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
SHA-256 de token, función y parámetros, así que el token nunca aparece en claro
en la clave ni en los logs, y un token distinto produce una clave distinta.

## Privacidad y minimización de datos

Este programa solo se conecta a AulaGlobal y, para la comprobación diaria de
versiones, a GitHub, sin enviar datos del usuario ni del curso. Lo que devuelven
las herramientas MCP llega al asistente conectado, que puede enviarlo a su
proveedor de IA según sus condiciones. Este programa no controla ese envío. Las
restricciones de carpeta (os.Root, rutas relativas) solo se aplican a las
herramientas de AulaGlobal MCP. Un asistente con acceso propio al sistema de
archivos tiene los permisos que tenga configurados. `setup` muestra este aviso
antes de conectar asistentes.

`auth_status` no devuelve el nombre completo ni el ID de usuario. Usa su propio
tipo `AuthOutput` en `internal/mcpserver`, separado de `aulaglobal.Auth`.
`aulaglobal.Auth` sí decodifica esos datos, pero solo para uso local: el saludo de
`setup` y el userid del respaldo de matrículas en Moodle antiguo. Ninguna otra
herramienta devuelve datos de la cuenta, y las calificaciones solo salen si se
pide `include_submission_status`, y los comentarios de las correcciones solo con
get_assignment_feedback, que además deja un evento `feedback_viewed` en los
registros del curso y puede devolver datos de terceros. El servidor MCP no define `instructions`, así
que no añade texto de privacidad al prompt del asistente.

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
Esto no demuestra cuál es la política ni la versión de UC3M, ni la fecha de este
token.

[get_site_info](https://github.com/moodle/moodle/blob/MOODLE_405_STABLE/webservice/externallib.php)
no expone validuntil.
[launch.php](https://github.com/moodle/moodle/blob/MOODLE_405_STABLE/admin/tool/mobile/launch.php)
envía firma:::token[:::privatetoken], sin fecha de creación/caducidad.
[get_config/get_public_config](https://github.com/moodle/moodle/blob/MOODLE_405_STABLE/admin/tool/mobile/classes/api.php)
no exponen en esa versión el validuntil del token; los ajustes de autologin no
son la caducidad del token WS. Por eso auth_status devuelve expires_at:null y
expiry_source:unknown..., nunca calcula 12 semanas desde la fecha de conversión.
Los errores invalidtoken e invalidtimedtoken indican un token caducado. Ante un
accessexception, el cliente llama a core_webservice_get_site_info para
confirmarlo. Si esa llamada también falla, el token está caducado y se devuelve
ErrTokenExpired, con un mensaje en español que explica cómo renovarlo. Si
funciona, es un problema de permisos y el mensaje lo dice. Con un token caducado,
download_files y list_assignments cancelan el lote con un único error en lugar de
un error por elemento.

`TokenProvider.Get(ctx)` permite añadir renovación sin cambiar la API ni las
herramientas, e `Invalidator` es una interfaz opcional. El proveedor estático
relee el archivo en cada petición, así que otro proceso puede rotarlo. Una
posible ampliación es un proveedor sincronizado que invalide el token al recibir
un error de autenticación y use un flujo de Moodle explícitamente compatible,
quizá el privatetoken para autologin. Hoy no hay renovación automática, y
privatetoken no se trata como un refresh token de OAuth.

Si la política tokenduration del sitio caduca el token (12 semanas por defecto
upstream), ejecuta de nuevo `aulaglobal-mcp setup`. Setup valida el token
guardado y, si ha caducado, salta directamente al paso de la cookie. `token-from-cookie --force` sigue
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

- `cmd/aulaglobal-mcp`: solo `main.go`. Reparte los subcomandos (añadir uno es
  un `case` y un paquete en `internal/cli`) y guarda `version`, que el
  enlazador rellena con `-X main.version=...`.
- `internal/cli/serve`, `setup`, `uninstall`, `update`: un paquete por comando.
  `setup` incluye también `token-from-cookie`, que comparte con él el guardado
  del token. Cada paquete trae sus pruebas y sus `testdata` (los `.golden`).
  `internal/cli/env` lee variables de entorno con valor por defecto.
- `internal/assistants`: la interfaz `Assistant`, la lista, un archivo por
  asistente (`claudecode.go`, `claudedesktop.go`, `codex.go`, `opencode.go`,
  `antigravity.go`), `file.go` (lo común a los asistentes con un solo archivo
  de configuración) y `config_json.go` / `config_write.go` (edición de
  configuraciones). `assistants/assistantstest` tiene los dobles de prueba
  (entorno temporal, `claude` falso, fixture) que usan también `setup` y
  `uninstall`.
- `internal/termui` (preguntas por terminal), `internal/atomicfile` (escritura
  atómica) e `internal/testutil` (ayudas de prueba comunes).
- `internal/clipboard`: portapapeles mediante comandos externos, sin dependencias nuevas; selección por sistema y entorno, con límite de dos segundos por comando.
- `internal/aulaglobal`: API de Moodle, token, cookie y acceso respetuoso.
- `internal/store`: descarga, lectura y búsqueda.
- `internal/mcpserver`: `server.go` registra las herramientas (nombre,
  descripción y manejador), `tools.go` tiene sus tipos y manejadores, y
  `http.go` la seguridad HTTP.
- `internal/selfupdate` y `internal/userpaths`: actualización firmada y rutas.

Para añadir una herramienta MCP: tipos de entrada y salida y un método en
`tools.go`, y una línea en `New` (`server.go`).

Para añadir un subcomando: un paquete `internal/cli/<nombre>` con una función
`Run` y un `case` en `run` (`cmd/aulaglobal-mcp/main.go`).

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
usa stdio en el escritorio, que no expone tus archivos de cursos ni el acceso a
AulaGlobal a través de una URL pública. Nunca publiques este servidor con la
autenticación HTTP desactivada.

Fuentes consultadas el 14 de noviembre de 2026; los menús pueden cambiar.


## Instalación nativa y compatibilidad

`aulaglobal-mcp setup [--secrets-dir RUTA] [--data-dir RUTA] [--no-pause]`
lee la cookie con entrada oculta (x/term) y verifica el token con Auth, sin curl.
Si stdin es un terminal, espera a que pulses Enter al terminar; `--no-pause` lo
evita. En Windows, si el programa se abre sin argumentos y con terminal, ejecuta
setup, para que funcione el doble clic.

Secretos predeterminados (os.UserConfigDir()/aulaglobal-mcp/token):
- Linux: $XDG_CONFIG_HOME/aulaglobal-mcp/token o ~/.config/aulaglobal-mcp/token.
- macOS: ~/Library/Application Support/aulaglobal-mcp/token.
- Windows: %APPDATA%/aulaglobal-mcp/token.

Los apuntes van a os.UserHomeDir()/AulaGlobal en los tres sistemas, sin depender
del cwd.
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

La configuración JSON de Claude se combina y conserva los valores ajenos,
incluidos los números grandes. Para TOML, github.com/pelletier/go-toml/v2 solo
valida y serializa el bloque nuevo; el resto del archivo no cambia al añadir. Se pide confirmación antes de sustituir.
La sustitución conservadora rechaza texto multilínea o tablas inline ambiguas;
en esos casos se edita a mano. Se usa el parser pequeño para no aceptar TOML
inválido ni duplicar tablas con claves entrecomilladas. Copia .bak antes de
sustituir; escritura con temporal en el mismo directorio.
Detección de clientes por sus carpetas de configuración; si todavía no existen,
abre primero la aplicación o usa los ejemplos manuales. No detecta cada variante
de empaquetado (por ejemplo, instalaciones sandbox).

Los manifiestos nuevos guardan las rutas con /, y al leer un manifiesto antiguo
se normalizan sus separadores. os.Root mantiene el confinamiento en cada OS.
Sanitize elimina caracteres reservados, puntos/espacios extremos y nombres DOS.
Skip compara segundos Unix exactos: en FAT con resolución de dos segundos puede
volver a descargar un archivo con fecha impar; es conservador y evita omitir
cambios. NTFS y APFS no necesitan tolerancia. Las entradas interactivas aceptan
CRLF. El manejo de SIGINT y SIGTERM compila en Windows, pero el cierre por señal
solo se puede probar en CI o en un host Unix.

## Actualización automática

`aulaglobal-mcp update [--check] [--yes] [--force]` está implementado en
internal/selfupdate (la CLI en internal/cli/update).

Flujo: consulta `GET https://api.github.com/repos/Alejandro12120/aulaglobal-mcp/releases/latest`
(sin autenticación, User-Agent `aulaglobal-mcp/<versión>`; excluye borradores y
prereleases), compara versiones semver estrictas (`vX.Y.Z`; nunca se baja de
versión) y, si hay una más reciente y el usuario confirma ([S/n], o `--yes`):

1. Descarga `checksums.txt` y `checksums.txt.sig` y verifica la firma con la
   clave embebida antes de fiarse de nada más.
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
  antigua que embeba la clave nueva; las siguientes pueden firmarse con la
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
copia con la línea `SIGN_KEY` sustituida (se comprueba que difiere solo en esa
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

`setup` separa carpetas, token, comprobación y asistentes en cuatro pasos. La
cookie se escribe con entrada oculta, y setup también comprueba el token que ya
estaba guardado.
En el paso 2 intenta copiar el comando JavaScript al portapapeles y confirma
el éxito con ✓; si no puede, indica cómo copiarlo a mano sin mostrar errores.
El comando siempre aparece en pantalla. Tras intercambiar la cookie por un
token, lee el portapapeles y solo lo vacía si coincide exactamente con la cookie;
solo entonces muestra «· Portapapeles vaciado.». Si leer o vaciar falla, continúa
sin avisos ni secretos en los errores. La lectura y el vaciado no son atómicos.

Usa `pbcopy`/`pbpaste` en macOS; `clip.exe` en Windows (allí no se lee ni
se vacía); `wl-copy`/`wl-paste --no-newline` si hay `WAYLAND_DISPLAY`; o,
con `DISPLAY`, `xclip -selection clipboard` y como alternativa `xsel`.
Los comandos se buscan en PATH; no instala nada ni usa SSH u OSC 52. Los datos
se pasan por stdin, nunca por argumentos. Cada comando tiene dos segundos
de plazo; las escrituras no tienen pipes de stdout/stderr que los procesos
hijos de Wayland/X11 puedan mantener abiertos. Setup recibe una interfaz
inyectable; sus pruebas usan un portapapeles falso y las del paquete comprueban
la selección con entorno y búsqueda de ejecutables simulados.
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
Se rechaza tener los dos archivos a la vez, el JSONC con comentarios o comas
finales, el JSON inválido y las estructuras ambiguas. Se muestran bloques manuales V1 y V2.
Se preservan valores desconocidos y servidores ajenos, reformateando JSON.
El original exacto se guarda en .bak; la escritura usa temporal y rename.
No se imprime la salida del CLI ni errores con posible contenido privado.

Prueba real en Docker node:22: npm opencode-ai 1.19.5 ofrece --env pero no
--global en mcp add --help. Las capturas están en internal/assistants/testdata.
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
`internal/assistants/testdata/agy-1.5.9-mcp-add-help.txt`.
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

`internal/assistants` define `Assistant`: ID, Name, Detect, Inspect, Apply,
Manual y Reload (y, para `uninstall`, Find, Remove y ManualRemove). La única lista ordenada `registry` (`assistants.All`) sirve para detección,
selección por ID, ayuda y pruebas de contrato. Los estados son no instalado,
falta, idéntico, diferente y otro ámbito. Los errores de inspección se cierran
sin escribir ni mostrar diagnósticos privados.

`configureClients` inspecciona antes de preguntar, muestra el inventario y usa
siempre [S/n] para añadir o actualizar. Con dos o más cambios propone configurar
todos; `e` permite elegir y `n` omite todos los pendientes. Las entradas idénticas
no preguntan. El resumen distingue añadido, actualizado, ya estaba, omitido y
falló, y solo imprime Reload de los clientes modificados. Las transcripciones en
`internal/cli/setup/testdata/setup-*.golden` fijan el formato.

Para añadir un asistente:

1. Crea `internal/assistants/<id>.go` con un tipo que implemente `Assistant` (o construye
   un `fileAssistant` con las particularidades del formato, ruta, detección y
   recarga). Ahí va todo lo propio del asistente, también quitar su entrada.
2. Añade una entrada a `registry`. Su ID queda disponible en --help y --clients.
3. Ejecuta las pruebas compartidas `TestAssistantContracts` (`internal/cli/setup/contracts_test.go`) y añade fixtures o
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

Implementada en `internal/cli/uninstall`: `uninstall.go` (opciones, menú y
orquestación), `plan.go` (qué se hará en cada opción), `path.go` (perfiles de
shell y PATH de Windows), `data.go` (token y carpeta de apuntes), `program.go`
(borrar el ejecutable), `system_windows.go` / `system_other.go` (registro y
proceso auxiliar de Windows). Quitar la entrada de cada asistente está en su
archivo de `internal/assistants`. Texto en español y
mismo estilo visual que setup (✓ ! ✗ ·, `[s/N]`).

### Opciones y flags

Sin flags y con terminal abre el menú: 1 desconectar asistentes (predeterminada),
2 cerrar la sesión (borrar la clave de acceso), 3 desinstalar todo. En todos los
casos se imprime el plan exacto y se pide `¿Continuar? [s/N]` (Enter = no);
después se imprime el resultado con ✓/·/!/✗ por elemento. Cualquier ✗ da salida
distinta de 0.

| Flag | Efecto |
|---|---|
| `--clients ids` | opción 1 para esos asistentes (IDs de setup), sin menú |
| `--logout` | opción 2 |
| `--all` | opción 3 (conserva la carpeta de apuntes) |
| `--delete-data` | con `--all`, permite borrar la carpeta de apuntes (reglas abajo) |
| `--yes` | omite las confirmaciones; no implica `--delete-data` ni quita entradas ajenas |
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
(instrucciones manuales). Solo se quitan entradas cuyo comando es este
ejecutable o un binario llamado `aulaglobal-mcp`; si la entrada difiere se
muestra y se pregunta (`[s/N]`). Con `--yes` o sin terminal las ajenas no se
tocan nunca (aviso `!`).

- Claude Code: `claude mcp remove --scope user aulaglobal` y comprobación
  posterior con `mcp get`. Los ámbitos local y proyecto se detectan y se
  explican (con el comando para quitar el local en su carpeta); no se tocan
  ni `.mcp.json` ni el ámbito local. Limitación: `mcp get` solo muestra el
  ámbito de mayor prioridad, de modo que una entrada de usuario tapada por una
  local no se ve desde esa carpeta. La salida del CLI nunca se imprime.
- Claude Desktop y Antigravity (`mcpServers`) y OpenCode (V2 `mcp.servers`, V1
  `mcp`): la entrada se corta por bytes (se localiza con el decodificador JSON
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
| Linux / macOS | Se buscan, en todos los perfiles que escribe install.sh (`~/.zshrc` o `$ZDOTDIR/.zshrc`, `~/.bashrc`, `~/.bash_profile`, `$XDG_CONFIG_HOME/fish/config.fish`, `~/.profile`), las líneas con la forma exacta del instalador y terminadas en `# aulaglobal-mcp`. Solo se quita la que añade la carpeta de este ejecutable (las de otra carpeta se dejan y se avisa). Copia en `<perfil>.bak`; se conservan permisos y enlaces simbólicos; idempotente | `os.Remove` del ejecutable (resuelto con EvalSymlinks); su carpeta solo si se llama `aulaglobal-mcp` y está vacía. La carpeta típica `~/.local/bin` nunca se borra |
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
Nunca se borra fuera de esa carpeta. El borrado se rechaza, con un aviso para
hacerlo a mano, si:
la ruta está vacía o no es absoluta, es la raíz, es la carpeta personal o la
contiene, contiene el programa/configuración/caché/token, es un enlace simbólico
o no es una carpeta, se llama como una carpeta personal del sistema (Documents,
Descargas, Dropbox...), o no contiene el directorio marca `.aulaglobal-cache` (una
carpeta vacía sí se puede quitar). Una carpeta que no es la predeterminada solo se
borra respondiendo `s` en una terminal, con un aviso extra (`--delete-data` no
basta). La comprobación se repite justo antes de borrar.

### Pruebas

`internal/cli/uninstall/*_test.go`: plantilla de instalación falsa en directorio temporal
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

## Benchmarks sin acceso a Moodle

Desde la raíz del repositorio, ejecuta `PROFILE=1 bash scripts/bench/run.sh`.
El script crea y elimina árboles de trabajo temporales para medir 927ab6c,
6f7bc19, 0626e64 y HEAD con la misma imagen `golang:1.26.9`. Una vez
descargados los módulos, los contenedores de medición se ejecutan con
`--network none`. Los benchmarks Go simulan Moodle en loopback y distinguen
el límite por defecto de 4 peticiones/s del máximo de 100 peticiones/s. Las
pruebas de proceso usan un HOME y un directorio de datos temporales, un token
ficticio y el chequeo de actualización desactivado. Solo llaman a herramientas
que trabajan con archivos locales.

Los resultados quedan en `.bench-results/`. `results.csv` indica la mediana y
el p95 de rango más próximo en microsegundos, y la mediana de bytes y
asignaciones por operación para los benchmarks Go. Cada muestra Go es
un promedio de las iteraciones de esa ejecución. La búsqueda recorre 300
archivos de unos 30 MiB en total. La prueba en proceso distingue la primera
extracción de la búsqueda con caché de extracción. El directorio también
contiene las salidas originales, los hashes de `tools/list` y, con
`PROFILE=1`, los perfiles de CPU y memoria y sus informes `pprof -top`.
El script compara el hash de HEAD con el de 0626e64.

Por defecto se toman 10 muestras Go y 20 muestras de proceso por escenario.
Se pueden ajustar con `BENCH_RUNS`, `BENCH_TIME` y `PROCESS_RUNS`. Para
repetir solo una revisión, pasa su etiqueta al script, por ejemplo
`PROFILE=1 bash scripts/bench/run.sh optimized`.
