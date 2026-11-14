# Verificación ejecutada — segunda iteración

2026-11-14, Linux amd64. Docker, sin Go en el host, sin credenciales reales,
sin autenticar contra UC3M. Implementación y comprobación sin subagentes.

## Análisis y tests

Comando ejecutado, exit 0:

```bash
docker run --rm -v "$PWD":/src -w /src \
  -v ag-go-cache:/root/.cache/go-build -v ag-go-mod:/go/pkg/mod \
  golang:1.26.9 sh -c 'gofmt -w cmd internal && go vet ./... && go test -race ./...'
```

go vet sin diagnósticos. Salida de la última ejecución:

```text
?    aulaglobal-mcp/cmd/aulaglobal-mcp [no test files]
ok   aulaglobal-mcp/internal/aulaglobal 1.053s
ok   aulaglobal-mcp/internal/mcpserver (cached)
ok   aulaglobal-mcp/internal/store 2.467s
```

Tests nuevos:
- PDF mínimo generado en testdata/tiny.pdf (Helvetica, una página, xref válido);
  extracción y búsqueda con página citada; PDFs malformados no derriban el proceso.
- docx, pptx, odt, odp creados con archive/zip; rechazo de ZIP expansivo,
  más de 2048 entradas, más de 500 páginas PDF y texto mayor de 8 MiB.
- Búsqueda después del byte 70000, filtro path_prefix, caché invalidada por mtime
  con tamaño idéntico, y acceso directo a caché rechazado.
- Descargas con httptest: rutas legibles/subcarpeta, colisión con sufijo,
  manifiesto tras reapertura, skip persistente, reemplazo de revisión en la misma
  ruta. Handle estable al cambiar la URL de revisión.
- Servidor que envía un byte y se bloquea: timeout de inactividad de 40 ms
  interrumpe la descarga y devuelve error en menos de un segundo.
- Entregas: cursos implícitos de timeline, POST courseids[0]/courseids[1],
  upcoming/overdue/all, orden, sin fecha al final, nombres bilingües, adjuntos,
  estado/nota y fallo individual; Europe/Madrid en invierno y verano.
- Secretos: rechazo sin --force, reemplazo 0600, metadatos y edad sin inferir
  expires_at.

Se conservan los tests anteriores de redacción, errores Moodle, restricciones
HTTP, cancelación, 200 descargas con cuatro workers, HTML/JSON erróneo,
UTF-8 y escapes por symlinks/os.Root.

## Docker y MCP

Comandos ejecutados, todos exit 0:

```bash
docker build -t aulaglobal-mcp:local .
node scripts/smoke.mjs
docker image inspect aulaglobal-mcp:local --format '{{.Id}} {{.Size}}'
git diff --check
```

Salida real del smoke:

```text
PASS stdio initialize + tools/list (7 input/output schemas) + auth_status missing token
PASS HTTP initialize + tools/list + auth_status; missing auth=401, hostile Origin/Host=403, oversized body=413
Image bytes: 14314905
```

Imagen:
sha256:c79396982b191351d16cc4fbcff4eceb861f2f40738a1c82b80128e18aeacd24

Tamaño reportado por Docker: **14314905 bytes (14.31 MB / 13.65 MiB)**.
Anterior registrado y comprobado antes de compilar: 13005700 bytes;
incremento reportado: 1309205 bytes (~10.1%). Sigue siendo scratch, UID 65532,
binario estático, CA y directorio /data. El binario extraído de la imagen mide
**9932962 bytes**; el tamaño de imagen reportado por Docker incluye su
contabilización de almacenamiento y no equivale al tamaño del binario.

Dependencia añadida: github.com/ledongthuc/pdf
v0.0.0-20261020125853-5e46805f718e. Go puro, BSD-3-Clause, ninguna dependencia
transitiva: evita herramientas externas y bibliotecas C.
Fuente: https://github.com/ledongthuc/pdf (consultada).
Medición controlada con golang:1.26.9 y también golang:1.26.9-alpine,
CGO_ENABLED=0, -buildvcs=false -trimpath -ldflags="-s -w": copia temporal del
código con/sin la rama PDF y su import, conservando ZIP, entregas y tzdata.
Binario con PDF: 9932962; sin PDF: 9650338; coste marginal **282624 bytes
(276 KiB)**. tzdata y ZIP/XML también contribuyen a la funcionalidad nueva.
La medición no modifica el árbol de trabajo.

## Límites y desviaciones

- Sin prueba con token real de AulaGlobal; permisos/versiones del sitio siguen
  pendientes de validación real. expires_at continúa null.
- Sin OCR, XLSX opcional ni extracción de formatos binarios Office antiguos.
- PDF recupera panics y devuelve en 5s, con dos slots globales; un parser que no
  termina retiene su slot. El parser no es cancelable internamente y puede
  consumir memoria antes de comprobar el límite de salida de una página.
  No se promete aislamiento de proceso ni límite duro de memoria.
- No se migran rutas del diseño anterior. Caché sin limpieza automática.
  El manifiesto conserva las rutas asignadas; las colisiones se resuelven en
  orden de handle dentro de cada lote y se mantienen entre reinicios.
- submissions_possible refleja fechas y nosubmissions; permisos, restricciones
  individuales y extensiones pueden cambiar el resultado efectivo.
- Los handles son estables, pero sus URLs actuales se registran en memoria:
  tras reiniciar hay que listar de nuevo antes de download_files por handle.
- El smoke valida contrato y transporte, no realiza descargas reales.
