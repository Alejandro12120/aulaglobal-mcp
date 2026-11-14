# Verificación ejecutada — follow-up de 0ff6e04

2026-11-14, Linux amd64. Trabajo realizado sin subagentes, sin Go en el host,
sin credenciales reales ni conexión autenticada a UC3M. Go: Docker golang:1.26.9.

## Regresiones antes y después

1. **No se reproduce ReadTimeout cancelando una llamada tras consumir el cuerpo.**
   TestHTTPReadTimeoutAndDisconnect (cmd/aulaglobal-mcp/main_test.go) usa un
   listener real de httptest y conexión TCP: POST con cuerpo completo, handler
   consume EOF y espera sobre r.Context(); cliente conectado durante 150 ms,
   con ReadTimeout de 40 ms. No hay cancelación; cerrar el cliente sí cancela.
   También prueba ReadTimeout=0. Resultado antes y después: PASS en ambas ramas.
   Se inspeccionó /usr/local/go/src/net/http/server.go de golang:1.26.9:
   connReader.startBackgroundRead llama cr.rwc.SetReadDeadline(time.Time{})
   antes de backgroundRead. Por ello se conserva ReadTimeout=30s, además de
   ReadHeaderTimeout=10s, IdleTimeout=60s, cabeceras 16 KiB y MaxBytesReader 1 MiB.
   No se introduce WriteTimeout.

2. **Reproducido:** con una única costura de inyección del extractor PDF, el test
   lento agotó el plazo del caller: context deadline exceeded, sin nota útil,
   y el resultado tardío no se guardó en caché (FAIL).
   TestLateExtractionCaches ahora prueba tanto el presupuesto local de espera
   como el deadline del caller; libera el fake después del timeout y verifica
   que un retry obtiene el resultado desde caché sin volver a invocar el fake.
   PASS. TestExtractTimeoutConfiguration valida default 60s, extremos 1/600 y
   rechazo de 0/601/negativos/texto. TestExtractionSlotBound mantiene dos fakes
   bloqueados y comprueba que el tercero no empieza; al terminar liberan slots.
   PASS. El presupuesto limita la espera, no mata el parser; éste guarda
   resultados exitosos mediante escritura atómica incluso tras desconexión.
   Se consideraron páginas parciales: se conserva el rechazo >500 páginas para
   evitar presentar documentos incompletos. Se conservan límites de entrada,
   texto, ZIP, confinamiento os.Root, recuperación de panics y dos slots.

3. **Reproducido:** TestIntroRuneLimitAndCanonicalNames fallaba con 2500 ASCII,
   1500 runas multibyte y 2500 runas multibyte. Ahora PASS: límite único de
   2000 runas más " [truncated]", sin truncar contenido menor al límite.

4. **Reproducido:** mismo test encontraba CourseName inglés en adjuntos y una
   consulta innecesaria de core_course_get_courses_by_field al listar archivos.
   Ahora PASS: display inglés, carpeta española, nombre reutilizado del caché
   protegido con mutex. Courses y Assignments alimentan ese caché.
   TestCanonicalFoldersAndReadableCollisions fallaba al reabrir el Store y
   registrar un archivo nuevo bajo otro nombre de curso. Ahora PASS:
   course_id/course_folder en manifiesto mantienen la carpeta original.
   Manifiestos antiguos se aceptan; un handle conocido permite recuperar su
   carpeta. No se mueven archivos ni se renombran rutas ya asignadas.

5. **Reproducido:** el test anterior recibía a (bbb).pdf en vez de a (2).pdf.
   Ahora PASS: orden estable por handle en cada lote, sufijos (2), (3), etc.,
   reservando archivos existentes y rutas de manifiesto. La reapertura y un
   lote posterior conservan la asignación previa. Rutas antiguas con sufijos
   de handle se mantienen para no romper referencias existentes.

## Comandos y resultados reales

Todos exit 0:

```bash
docker run --rm -v "$PWD":/src -w /src \
  -v ag-go-cache:/root/.cache/go-build -v ag-go-mod:/go/pkg/mod \
  golang:1.26.9 sh -c 'gofmt -w cmd internal && go vet ./... && go test -race ./...'
```

go vet sin diagnósticos. Última salida:

```text
ok   aulaglobal-mcp/cmd/aulaglobal-mcp (cached)
ok   aulaglobal-mcp/internal/aulaglobal (cached)
ok   aulaglobal-mcp/internal/mcpserver (cached)
ok   aulaglobal-mcp/internal/store 2.762s
```

La ejecución anterior, también con race, fue completamente sin caché:
cmd 1.320s, aulaglobal 1.070s, mcpserver 1.017s, store 2.792s.

```bash
docker build -t aulaglobal-mcp:local .
node scripts/smoke.mjs
docker image inspect aulaglobal-mcp:local --format '{{.Id}} {{.Size}}'
git diff --check
```

Build completado (builder legacy muestra aviso de deprecación). Smoke:

```text
PASS stdio initialize + tools/list (7 input/output schemas) + auth_status missing token
PASS HTTP initialize + tools/list + auth_status; missing auth=401, hostile Origin/Host=403, oversized body=413
Image bytes: 14326383
```

Imagen: sha256:67ea18725ff1b22cf4c727eff0f9df1d536ca4eb463f593bcb60bb488d4d0b42

Tamaño Docker: **14326383 bytes (14.33 MB / 13.66 MiB)**.
Incremento frente al anterior 14314905: 11478 bytes. Continúa scratch,
UID 65532, binario estático, certificados CA y /data.

## Límites pendientes

Sin validación con un token real, sin OCR ni aislamiento de proceso del parser.
Un parser que nunca termina retiene su slot y puede consumir memoria antes de
comprobar el límite de salida de una página. Caché sin limpieza automática.
Los handles requieren volver a listar tras reiniciar para registrar sus URLs.
La cancelación del caller sigue devolviendo error de contexto; el mensaje
"extraction in progress, retry shortly" corresponde al presupuesto del Store.

## Instalación nativa y Releases — 2026-11-14

Trabajo sin subagentes, sin push, sin crear repositorios y sin contactar con
UC3M. Se conservaron el tono del README, los agradecimientos a AGDownloader y
las secciones técnicas existentes. El HEAD encontrado fue 093bf4a (la guía
simplificada ya estaba confirmada), en lugar del 14921d3 indicado en el encargo;
no se retrocedió ni se sobrescribió ese trabajo.

### Comprobaciones reproducibles

Go del host no utilizado. Compilador: golang:1.26.9.
En los contenedores locales se pasó safe.directory=/src mediante GIT_CONFIG_*,
porque el checkout montado pertenece al usuario del host.

```sh
docker run --rm -v "$PWD":/src -w /src \
  -v ag-go:/go -v ag-cache:/root/.cache/go-build \
  -e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory \
  -e GIT_CONFIG_VALUE_0=/src golang:1.26.9 sh -c '
    go vet ./... && go test -race ./... &&
    for target in windows darwin; do
      GOOS=$target go vet ./... || exit
      for pkg in $(go list ./...); do
        GOOS=$target go test -c -o /tmp/target-test "$pkg" || exit
      done
    done'
docker run --rm -v "$PWD":/src -w /src rhysd/actionlint:1.7.7
docker run --rm -v "$PWD":/mnt koalaman/shellcheck:v0.10.0 obtener-token.sh
docker build -t aulaglobal-mcp:local .
node scripts/smoke.mjs
```

Resultados: vet Linux y race PASS en los cinco paquetes; vet y test -c para
windows/amd64 y darwin/amd64 PASS. La compilación cruzada no ejecuta tests nativos.
actionlint y shellcheck: salida vacía, código 0. Docker build: código 0.
No se usa el antiguo test Python del helper: la lógica pasó a setup_test.go,
con tres entradas vacías, normalización, fake Moodle HTTP, conversión con firma
y verificación Auth, metadatos, rechazo a sustituir, limpieza acotada,
JSON con números grandes, otros servidores, TOML con claves entrecomilladas y
subtablas, confirmaciones y backups de ambos clientes. userpaths prueba Defaults
y las ubicaciones Claude de los tres OS. Los tests de permisos omiten únicamente
bits POSIX en Windows; el test de symlinks hace Skip si el host no permite crearlos.
TestManifestPortablePaths verifica separadores Windows antiguos y salida con /.

Go 1.26.9: comprobado directamente en
/usr/local/go/src/internal/syscall/windows/syscall_windows.go:
Rename llama MoveFileEx(from, to, MOVEFILE_REPLACE_EXISTING).
Esto permite sustituir archivos existentes; os.Rename no promete atomicidad
en plataformas no Unix. Skip usa igualdad exacta de segundos: FAT puede provocar
redescargas conservadoras. Falta ejecución nativa Windows/macOS, que cubre CI.

### GoReleaser

Imagen fijada: goreleaser/goreleaser:v2.8.2.
La imagen trae Go 1.24.1 con GOTOOLCHAIN=local: para el snapshot se pasa
GOTOOLCHAIN=go1.26.9. El primer intento detectó ese desfase y se corrigió.
No hay remoto: se pasa una URL ficticia solo en el entorno del comando,
sin modificar .git/config ni crear repositorios. En GitHub se usa el remoto real.

```sh
docker run --rm -v "$PWD":/src -w /src \
  -v ag-go:/go -v ag-cache:/root/.cache/go-build \
  -e GOTOOLCHAIN=go1.26.9 \
  -e GIT_CONFIG_COUNT=2 \
  -e GIT_CONFIG_KEY_0=remote.origin.url \
  -e GIT_CONFIG_VALUE_0=https://github.com/local-validation/aulaglobal-mcp.git \
  -e GIT_CONFIG_KEY_1=safe.directory -e GIT_CONFIG_VALUE_1=/src \
  goreleaser/goreleaser:v2.8.2 check
# Mismas opciones Docker, sustituyendo check por:
# release --snapshot --clean --skip=publish
```

check: «1 configuration file(s) validated», código 0.
Snapshot: CGO_ENABLED=0; linux, darwin, windows × amd64, arm64.
No se publican los resultados. README y docs/TECNICO.md están en cada archivo;
no se añade LICENSE porque no existe. checksums.txt acompaña los seis archivos.

Resueltos después: enlace Releases (Alejandro12120/aulaglobal-mcp), licencia MIT
(AGDownloader también es MIT y está acreditado), remoto configurado y CI nativa.
Pendientes: firma Windows/notarización Apple. GHCR y attestations se dejan fuera.
TOML con texto multilínea o definición inline del servidor existente se rechaza
al reemplazar para evitar pérdida de contenido; se ofrece configuración manual.

### Resultado final del snapshot

Versión: 0.0.0-SNAPSHOT-7320c9c. release succeeded after 5s, código 0.

| Archivo | Bytes |
|---|---:|
| aulaglobal-mcp_0.0.0-SNAPSHOT-7320c9c_linux_arm64.tar.gz | 3744395 |
| aulaglobal-mcp_0.0.0-SNAPSHOT-7320c9c_linux_x64.tar.gz | 4113619 |
| aulaglobal-mcp_0.0.0-SNAPSHOT-7320c9c_macos_apple-silicon.tar.gz | 3882460 |
| aulaglobal-mcp_0.0.0-SNAPSHOT-7320c9c_macos_intel.tar.gz | 4178533 |
| aulaglobal-mcp_0.0.0-SNAPSHOT-7320c9c_windows_arm64.zip | 3802767 |
| aulaglobal-mcp_0.0.0-SNAPSHOT-7320c9c_windows_x64.zip | 4231962 |

SHA256 de los seis archivos: OK. Contenidos README.md y docs/TECNICO.md: OK.
Binario linux/amd64 --version: 0.0.0-SNAPSHOT-7320c9c.

Salida final de las pruebas Linux y compilación cruzada:

```text
ok  aulaglobal-mcp/cmd/aulaglobal-mcp  1.365s
ok  aulaglobal-mcp/internal/aulaglobal (cached)
ok  aulaglobal-mcp/internal/mcpserver (cached)
ok  aulaglobal-mcp/internal/store (cached)
ok  aulaglobal-mcp/internal/userpaths (cached)
Cross vet and test -c: windows
Cross vet and test -c: darwin
```

Docker build final: Successfully built 75259d4a5740,
Successfully tagged aulaglobal-mcp:local. Smoke final:

```text
PASS stdio initialize + tools/list (7 input/output schemas) + auth_status missing token
PASS HTTP initialize + tools/list + auth_status; missing auth=401, hostile Origin/Host=403, oversized body=413
Image bytes: 14784534

```
