<div align="center">

# AulaGlobal MCP

**Conecta tu asistente de IA con AulaGlobal (UC3M)**

[Instalación](#instalación) · [Uso](#uso) · [Problemas frecuentes](#problemas-frecuentes) · [Seguridad y privacidad](#seguridad-y-privacidad) · [Documentación técnica](docs/TECNICO.md)

</div>

---

AulaGlobal MCP permite que tu asistente de IA consulte tus asignaturas de
[AulaGlobal](https://aulaglobal.uc3m.es), descargue sus materiales y trabaje con
ellos. Funciona con Claude Code, Claude Desktop, Codex, ChatGPT (aplicación de
escritorio), OpenCode y Antigravity.

| Función | Descripción |
|---|---|
| Asignaturas | Lista las asignaturas actuales, pasadas o futuras. |
| Materiales | Muestra y descarga los archivos de cada asignatura, organizados por tema. |
| Lectura y búsqueda | Lee PDF, Word, PowerPoint y texto, y busca en todo lo descargado. |
| Entregas | Muestra fechas, enunciados y, si se solicita, el estado y la calificación. |

Algunos ejemplos de peticiones:

> ¿Qué entregas tengo pendientes esta semana?
>
> Descarga los apuntes de Sistemas Operativos y resume el tema 3.
>
> Busca en mis apuntes dónde se explica el algoritmo de Dijkstra.

> [!IMPORTANT]
> **Proyecto no oficial**, sin relación con la Universidad Carlos III de Madrid.
> Úsalo solo con tu propia cuenta, respetando la normativa de la universidad y
> los derechos de autor de los materiales.

---

## Instalación

### Requisitos

- Windows, macOS o Linux.
- Uno de los asistentes compatibles indicados arriba.

No es necesario instalar Go, Docker ni WSL.

### 1. Descarga

Descarga el archivo correspondiente a tu equipo desde la
[página de versiones](https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest):

| Equipo | Archivo |
|---|---|
| Windows (Intel/AMD) | `…_windows_x64.zip` |
| Windows (ARM) | `…_windows_arm64.zip` |
| Mac con chip Apple (M1 o posterior) | `…_macos_apple-silicon.tar.gz` |
| Mac con procesador Intel | `…_macos_intel.tar.gz` |
| Linux (x86-64) | `…_linux_x64.tar.gz` |
| Linux (ARM) | `…_linux_arm64.tar.gz` |

En Mac, el tipo de procesador aparece en **menú Apple → Acerca de este Mac**.

Descomprime el archivo y guarda el programa en una carpeta definitiva: la
configuración de los asistentes apuntará a esa ubicación.

### 2. Configuración

Abre una terminal en la carpeta del programa y ejecuta:

| Sistema | Comando |
|---|---|
| Windows (PowerShell) | `.\aulaglobal-mcp.exe setup` |
| macOS y Linux | `./aulaglobal-mcp setup` |

En Windows también puede abrirse con doble clic. En macOS y Linux, si aparece un
error de permisos, ejecuta antes `chmod +x aulaglobal-mcp`.

<details>
<summary><strong>Aviso del sistema al abrir el programa</strong></summary>

Los ejecutables aún no están firmados digitalmente, por lo que el sistema puede
mostrar un aviso la primera vez. Descarga el programa solo desde la página
oficial del proyecto.

- **Windows:** selecciona **Más información → Ejecutar de todas formas**.
- **macOS:** haz clic derecho sobre el programa y elige **Abrir**. Si continúa
  bloqueado, ejecuta en la carpeta del programa:

  ```sh
  xattr -d com.apple.quarantine aulaglobal-mcp
  ```

</details>

El asistente de configuración realiza cuatro pasos:

1. **Carpetas.** Indica dónde se guardarán la clave de acceso y los materiales.
2. **Cuenta de AulaGlobal.** Solicita la cookie de sesión del navegador y la
   convierte en una clave de acceso. Las instrucciones se muestran en pantalla.
3. **Comprobación.** Verifica la clave y muestra el nombre del titular de la cuenta.
4. **Asistentes.** Detecta los asistentes instalados y los configura (ver abajo).

<details>
<summary><strong>Cómo obtener la cookie de sesión</strong> (el programa de configuración te guía en este paso; estas instrucciones son solo de referencia)</summary>

1. Inicia sesión en <https://aulaglobal.uc3m.es>.
2. Abre las herramientas de desarrollador con **F12** (en Mac,
   **Cmd + Option + I**) y selecciona la pestaña **Consola**.
3. Pega la siguiente línea y pulsa **Enter**. El valor se copia al portapapeles:

   ```js
   copy(("; " + document.cookie).split("; MoodleSessionag=").pop().split(";").shift())
   ```

4. Vuelve a la terminal y pégalo cuando se solicite. Por seguridad, el texto no
   se muestra mientras se escribe.

Si Chrome impide pegar en la consola, escribe `allow pasting` y pulsa Enter.
Si la línea no funciona, el valor también puede copiarse desde
**Aplicación → Cookies → https://aulaglobal.uc3m.es → `MoodleSessionag`**
(en Firefox, **Almacenamiento → Cookies**).

</details>

> [!WARNING]
> La cookie da acceso a tu cuenta mientras la sesión esté abierta. No la
> compartas ni la pegues en el chat del asistente: introdúcela únicamente en el
> programa de configuración.

### 3. Conexión con los asistentes

Durante el paso 4, el programa muestra los asistentes encontrados y comprueba la
configuración de cada uno:

| Situación | Acción |
|---|---|
| AulaGlobal no está configurado | Pregunta si se debe añadir. |
| Ya está configurado correctamente | No realiza cambios. |
| Existe una configuración distinta | Muestra la diferencia y pregunta si se debe actualizar. |
| El archivo no puede modificarse con seguridad | Muestra las instrucciones para hacerlo manualmente. |

Antes de modificar un archivo se guarda una copia con extensión `.bak`. Al
finalizar se muestra un resumen por asistente y, para los que han cambiado, cómo
recargarlos.

Para comprobar la conexión, escribe `/mcp` en el asistente: debe aparecer
`aulaglobal`.

**Opciones adicionales**

| Opción | Efecto |
|---|---|
| `--only-clients` | Configura solo los asistentes, sin repetir los pasos de la cuenta. |
| `--clients claude-code,codex` | Limita la configuración a los asistentes indicados. |
| `--yes` | Acepta los cambios propuestos sin preguntar. |
| `--help` | Muestra la ayuda y los identificadores de asistentes disponibles. |

Ejemplo: `./aulaglobal-mcp setup --only-clients --clients claude-code --yes`

---

## Uso

Basta con hacer las peticiones en lenguaje natural. Para comprobar que todo
funciona:

1. «Comprueba mi conexión con AulaGlobal.»
2. «¿Qué asignaturas tengo este cuatrimestre?»
3. «¿Qué materiales hay en Cálculo?»
4. «Descarga los PDF de Cálculo y resume el primero.»
5. «¿Qué entregas tengo pendientes y cuáles he entregado?»

Los materiales se guardan en la carpeta `AulaGlobal` de tu usuario, organizados
por asignatura y sección:

```text
AulaGlobal/
└── Cálculo/
    ├── Tema 1/
    │   └── apuntes.pdf
    └── Entregas/
        └── Práctica 1/
            └── enunciado.pdf
```

En descargas posteriores solo se obtienen los archivos nuevos o modificados.

---

## Configuración manual

Solo es necesaria si el programa de configuración no detecta tu asistente. En los
ejemplos, sustituye `/ruta/al/aulaglobal-mcp` por la ruta completa del programa
(en Windows termina en `aulaglobal-mcp.exe` y pueden usarse barras `/`, por
ejemplo `C:/Users/Ana/Programas/aulaglobal-mcp.exe`).

<details>
<summary><strong>Claude Code</strong></summary>

```sh
claude mcp add --scope user aulaglobal -- "/ruta/al/aulaglobal-mcp" --transport stdio
```

Como alternativa, para un único proyecto, crea un archivo `.mcp.json` en su
carpeta:

```json
{
  "mcpServers": {
    "aulaglobal": {
      "command": "/ruta/al/aulaglobal-mcp",
      "args": ["--transport", "stdio"]
    }
  }
}
```

</details>

<details>
<summary><strong>Claude Desktop</strong></summary>

Abre **Ajustes → Desarrollador → Editar configuración** y añade el bloque
`aulaglobal` del ejemplo de Claude Code dentro de `mcpServers`, conservando el
resto. Ubicación del archivo:

| Sistema | Ruta |
|---|---|
| Windows | `%APPDATA%\Claude\claude_desktop_config.json` |
| macOS | `~/Library/Application Support/Claude/claude_desktop_config.json` |
| Linux | `~/.config/Claude/claude_desktop_config.json` |

Reinicia la aplicación después de guardar.

</details>

<details>
<summary><strong>Codex y ChatGPT (aplicación de escritorio)</strong></summary>

Ambos comparten la configuración. Añade lo siguiente a `~/.codex/config.toml`
(en Windows, `%USERPROFILE%\.codex\config.toml`):

```toml
[mcp_servers.aulaglobal]
command = "/ruta/al/aulaglobal-mcp"
args = ["--transport", "stdio"]
```

Reinicia la aplicación. Esta configuración no se aplica a ChatGPT en el
navegador ([más información](docs/TECNICO.md#chatgpt-en-la-web)).

</details>

<details>
<summary><strong>OpenCode</strong></summary>

Si la versión instalada lo admite:

```sh
opencode mcp add aulaglobal --global -- /ruta/al/aulaglobal-mcp --transport stdio
```

En caso contrario, edita `~/.config/opencode/opencode.json` (o `opencode.jsonc`;
la ruta es la misma en Windows y macOS) y añade la entrada dentro de
`mcp.servers`, conservando los demás servidores:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "aulaglobal": {
        "type": "local",
        "command": ["/ruta/al/aulaglobal-mcp", "--transport", "stdio"]
      }
    }
  }
}
```

En versiones antiguas (V1), la entrada va directamente dentro de `mcp` e incluye
`"enabled": true`. Reinicia OpenCode después del cambio.

</details>

<details>
<summary><strong>Antigravity</strong></summary>

En el panel del agente, abre **… → MCP Servers → Manage MCP Servers → View raw
config** y añade la entrada dentro de `mcpServers`:

```json
{
  "mcpServers": {
    "aulaglobal": {
      "command": "/ruta/al/aulaglobal-mcp",
      "args": ["--transport", "stdio"]
    }
  }
}
```

El archivo se encuentra en `~/.gemini/config/mcp_config.json` (en Windows,
`%USERPROFILE%\.gemini\config\mcp_config.json`). Para aplicar el cambio, usa
**Restart** en **Customizations → Installed → MCP Servers**, o recarga desde
`/mcp` en el terminal.

</details>

Si en la configuración elegiste carpetas distintas de las predeterminadas, utiliza
el bloque que muestra el programa al finalizar, ya que incluye esas rutas.

---

## Problemas frecuentes

| Mensaje o síntoma | Solución |
|---|---|
| «La sesión de AulaGlobal ha caducado…» | El token ha caducado (Moodle los caduca periódicamente). Inicia sesión en AulaGlobal en el navegador y ejecuta de nuevo `aulaglobal-mcp setup`: detecta el token caducado y te pide una cookie nueva. No hace falta reiniciar el asistente. |
| «El token de AulaGlobal es válido, pero Moodle ha denegado el acceso…» | No es una caducidad: Moodle no permite esa función con tu cuenta o servicio. No hace falta renovar el token. |
| `cannot read token file` | Ejecuta de nuevo `setup`. Si usaste otras carpetas, revisa la configuración manual. |
| El asistente no muestra `aulaglobal` | Reinicia el asistente y, si persiste, ejecuta `setup --only-clients`. |
| No se puede obtener la clave | Cierra sesión en AulaGlobal, vuelve a entrar y repite el paso 2 con una cookie nueva. |
| Un PDF no se puede leer | Los PDF escaneados no contienen texto. Los muy extensos pueden tardar: repite la petición pasados unos segundos. |

---

## Seguridad y privacidad

- **Tu contraseña no se utiliza.** El acceso se obtiene a partir de la sesión del
  navegador.
- **La clave de acceso permanece en tu equipo**, en la carpeta de configuración
  de tu usuario y con permisos restringidos. El asistente de IA nunca la recibe.
- **Acceso limitado a tus archivos.** Las herramientas que usa el asistente solo
  pueden leer y escribir dentro de la carpeta `AulaGlobal`.
- **Sin intermediarios.** El programa se comunica directamente con AulaGlobal
  desde tu equipo.

---

## Agradecimientos

Este proyecto parte de
[AGDownloader](https://github.com/Astrak00/AGDownloader), de
[Eduardo Alarcón (Astrak00)](https://github.com/Astrak00), un descargador de
AulaGlobal para terminal publicado con licencia MIT. De él proceden el método de
obtención del acceso a partir de la sesión del navegador, la línea para la
consola y el tratamiento de los nombres de las asignaturas.

## Licencia

Distribuido bajo la [licencia MIT](LICENSE). El software se proporciona
**tal cual, sin garantía de ningún tipo**.

## Documentación técnica

El documento [docs/TECNICO.md](docs/TECNICO.md) describe el modo servidor HTTP
con Docker, las opciones de configuración, el modelo de seguridad, la
arquitectura del programa de configuración y el proceso de desarrollo y
publicación.
