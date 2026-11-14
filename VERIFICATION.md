# Verificación ejecutada

2026-11-14, Linux amd64, Docker 29.9.3. Sin Go instalado en el host,
sin credenciales reales y sin autenticar contra UC3M.

## Análisis y tests

Comando ejecutado (cachés Docker reutilizables, imagen Debian para CGO/race):

```bash
docker run --rm -v "$PWD":/src -w /src   -v ag-go-cache:/root/.cache/go-build -v ag-go-mod:/go/pkg/mod   golang:1.26.9 sh -c   'gofmt -w cmd internal && go vet ./... && go test -race ./...'
```

Resultado final: exit 0; go vet sin diagnósticos.
cmd/aulaglobal-mcp sin tests unitarios (cubierto por smoke).
internal/aulaglobal, internal/mcpserver e internal/store: PASS, sin carreras.
También se ejecutó go test ./... durante la compilación inicial.

Cobertura de escenarios: errores WS HTTP 200 y autenticación, fallback,
catálogo sin URLs/tokens, nombres bilingües, exclusiones sin inclusiones,
permisos de secretos, launch redirect y firma, redacción de errores/logs,
redirecciones y hosts externos rechazados, 200 descargas con cuatro workers,
skip-if-unchanged, cancelación, HTML/JSON/tamaños incorrectos, sanitización,
colisiones entre y dentro de módulos, symlinks fuera del root, lectura UTF-8
paginada/HTML/binarios y snippets acotados.

## Docker y MCP

```bash
docker build -t aulaglobal-mcp:local .
node scripts/smoke.mjs
docker image inspect aulaglobal-mcp:local --format '{{.Size}}'
```

Build: exit 0. Imagen scratch, no root, CA + binario estático.
Imagen comprobada:
sha256:3671171b2ce6b77c9a8e3cc92d537c12784f2b99f44c056f37f3238b05a86ac5

Tamaño local sin comprimir: **13005700 bytes (13.01 MB / 12.40 MiB)**.

Salida final del smoke (exit 0):

```text
PASS stdio initialize + tools/list (6 input/output schemas) + auth_status missing token
PASS HTTP initialize + tools/list + auth_status; missing auth=401, hostile Origin/Host=403, oversized body=413
Image bytes: 13005700
```

El smoke usa docker run -i y JSON-RPC por stdin para stdio; inicia otro
contenedor en HTTP con bearer ficticio, hace initialize/tools/list/auth_status,
comprueba controles HTTP y para/elimina el contenedor. No llama al sitio real
porque no hay token Moodle configurado.

git diff --check: sin errores.

## Límites explícitos

PDF: metadatos; sin extracción ni recursos MCP opcionales. Búsqueda simple,
primeros 64 KiB de texto por archivo UTF-8 hasta 8 MiB, 10000 entradas y 100
resultados. HTML usa limpieza simple. Los handles están en memoria.
Fallback de cursos devuelve todas las matrículas si la API temporal no existe.
No se ha probado con una sesión real de AulaGlobal. La caducidad exacta queda
unknown/null; las fuentes Moodle y la explicación están en README.
