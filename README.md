<div align="center">

# AulaGlobal MCP

**Conecta tu asistente de IA con AulaGlobal (UC3M)**

Español · [English](README.en.md)

[Instalación](#instalación) · [Uso](#uso) · [Actualización](#actualización) · [Problemas frecuentes](#problemas-frecuentes) · [Seguridad y privacidad](#seguridad-y-privacidad) · [Condiciones de uso](#condiciones-de-uso) · [Desinstalación](#desinstalación)

</div>

---

AulaGlobal MCP permite que tu asistente de IA consulte tus asignaturas de
[AulaGlobal](https://aulaglobal.uc3m.es), descargue sus materiales y trabaje con
ellos. Funciona con Claude Code, Claude Desktop, Codex, ChatGPT (aplicación de
escritorio), OpenCode y Antigravity.

| Función | Qué hace |
|---|---|
| Asignaturas | Lista tus asignaturas actuales, pasadas o futuras. |
| Materiales | Muestra y descarga los archivos de cada asignatura, organizados por tema. |
| Lectura y búsqueda | Lee PDF, Word, PowerPoint, Excel, LibreOffice y texto, y busca en todo lo descargado. Conserva títulos, listas, tablas y notas del orador. |
| Entregas | Muestra fechas, enunciados y, si lo pides, el estado y la calificación. |

Algunos ejemplos de lo que puedes pedir:

> ¿Qué entregas tengo pendientes esta semana?
>
> Descarga los apuntes de Sistemas Operativos y resume el tema 3.
>
> Busca en mis apuntes dónde se explica el algoritmo de Dijkstra.

> [!IMPORTANT]
> **Proyecto no oficial**, sin relación con la Universidad Carlos III de Madrid.
> Úsalo solo con tu propia cuenta y respeta la normativa de la universidad y
> los derechos de autor de los materiales.

---

## Instalación

Necesitas un ordenador con Windows, macOS o Linux y uno de los asistentes
compatibles. No hace falta instalar nada más.

Hay dos formas de instalarlo. La **instalación automática** es la más sencilla y
sirve en la mayoría de los casos.

### Opción A: instalación automática (recomendada)

Abre una terminal, copia el comando de tu sistema, pégalo y pulsa **Enter**.

**macOS y Linux** (aplicación **Terminal**):

```sh
curl -fsSL https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest/download/install.sh | sh
```

**Windows** (busca **PowerShell** en el menú Inicio y ábrelo):

```powershell
irm https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest/download/install.ps1 | iex
```

El instalador hace lo siguiente y pide confirmación cuando hace falta:

1. Detecta tu sistema y descarga la última versión.
2. Comprueba la firma digital y la integridad de la descarga. Si algo no
   coincide, se detiene sin instalar nada.
3. Te pregunta dónde instalar el programa y te propone una carpeta. Pulsa
   **Enter** para aceptarla:
   - macOS y Linux: `~/.local/bin`
   - Windows: `%LOCALAPPDATA%\Programs\aulaglobal-mcp`
4. Te ofrece añadir esa carpeta al PATH, para que puedas usar el comando
   `aulaglobal-mcp` desde cualquier terminal.
5. Abre el [programa de configuración](#configuración), que conecta tu cuenta y
   tus asistentes.

Si el programa ya estaba instalado, el instalador lo detecta y lo sustituye por
la versión nueva.

<details>
<summary><strong>Opciones avanzadas</strong></summary>

El instalador acepta estas variables de entorno:

| Variable | Efecto |
|---|---|
| `AULAGLOBAL_VERSION=v0.1.5` | Instala esa versión en lugar de la última. |
| `AULAGLOBAL_INSTALL_DIR=/ruta` | Instala en esa carpeta sin preguntar. |
| `AULAGLOBAL_YES=1` | Acepta todas las opciones propuestas sin preguntar. |

Ejemplo en macOS y Linux:

```sh
curl -fsSL https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest/download/install.sh | AULAGLOBAL_VERSION=v0.1.5 sh
```

Ejemplo en Windows:

```powershell
$env:AULAGLOBAL_VERSION = "v0.1.5"; irm https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest/download/install.ps1 | iex
```

El instalador comprueba la firma con `ssh-keygen`, que viene incluido en macOS,
en la mayoría de distribuciones Linux y en Windows 10 y 11. Si tu equipo no lo
tiene, el instalador explica cómo añadirlo. También puedes usar la instalación
manual.

</details>

### Opción B: instalación manual

Si prefieres no ejecutar un script, puedes descargar el programa tú mismo.

**1. Descarga** el archivo de tu equipo desde la
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

**2. Descomprime** el archivo y guarda el programa en una carpeta definitiva,
porque los asistentes lo buscarán en esa ruta.

**3. Abre una terminal en esa carpeta** y ejecuta el programa de configuración:

| Sistema | Comando |
|---|---|
| Windows (PowerShell) | `.\aulaglobal-mcp.exe setup` |
| macOS y Linux | `./aulaglobal-mcp setup` |

En Windows también puedes abrirlo con doble clic. En macOS y Linux, si aparece un
error de permisos, ejecuta antes `chmod +x aulaglobal-mcp`.

<details>
<summary><strong>Si el sistema muestra un aviso al abrir el programa</strong></summary>

El programa todavía no está firmado por Apple ni por Microsoft, así que al
descargarlo con el navegador el sistema puede mostrar un aviso la primera vez.
Descárgalo solo desde la página oficial del proyecto. La instalación automática
no muestra este aviso.

- **Windows:** selecciona **Más información → Ejecutar de todas formas**.
- **macOS:** haz clic derecho sobre el programa y elige **Abrir**. Si sigue
  bloqueado, ejecuta en la carpeta del programa:

  ```sh
  xattr -d com.apple.quarantine aulaglobal-mcp
  ```

</details>

### Configuración

El programa de configuración se abre solo al terminar la instalación automática.
Puedes volver a abrirlo en cualquier momento con `aulaglobal-mcp setup`. Tiene
cuatro pasos:

1. **Carpetas.** Indica dónde se guardarán la clave de acceso y los materiales.
2. **Cuenta de AulaGlobal.** Te pide la cookie de sesión del navegador y la
   convierte en una clave de acceso. Las instrucciones aparecen en pantalla.
   Si el sistema lo permite, setup copia el comando al portapapeles por ti.
   Cuando ya tiene la clave, vacía el portapapeles si todavía contiene la cookie.
3. **Comprobación.** Verifica la clave y muestra el nombre de la cuenta.
4. **Asistentes.** Detecta los asistentes instalados y te ofrece conectarlos.

<details>
<summary><strong>Cómo obtener la cookie de sesión</strong> (el programa de configuración te guía en este paso; estas instrucciones son solo de referencia)</summary>

1. Inicia sesión en <https://aulaglobal.uc3m.es>.
2. Abre las herramientas de desarrollador con **F12** (en Mac,
   **Cmd + Option + I**) y selecciona la pestaña **Consola**.
3. Pega esta línea y pulsa **Enter**. El valor se copia al portapapeles:

   ```js
   copy(("; " + document.cookie).split("; MoodleSessionag=").pop().split(";").shift())
   ```

4. Vuelve a la terminal y pégalo cuando se te pida. Por seguridad, el texto no
   se ve mientras lo pegas.

Si Chrome no te deja pegar en la consola, escribe `allow pasting` y pulsa Enter.
Si la línea no funciona, también puedes copiar el valor desde
**Aplicación → Cookies → https://aulaglobal.uc3m.es → `MoodleSessionag`**
(en Firefox, **Almacenamiento → Cookies**).

</details>

> [!WARNING]
> La cookie da acceso a tu cuenta mientras la sesión esté abierta. No la
> compartas ni la pegues en el chat del asistente. Introdúcela solo en el
> programa de configuración.

En el paso 4, el programa revisa cada asistente que encuentra:

| Situación | Qué hace |
|---|---|
| AulaGlobal no está conectado | Pregunta si quieres añadirlo. |
| Ya está conectado correctamente | No cambia nada. |
| Hay una configuración distinta | Muestra la diferencia y pregunta si quieres actualizarla. |
| El archivo no se puede modificar con seguridad | Muestra cómo hacerlo a mano. |

Antes de modificar un archivo, guarda una copia con extensión `.bak`. Al
terminar, muestra un resumen y explica cómo recargar cada asistente.

Para comprobar que funciona, escribe `/mcp` en el asistente. Debería aparecer
`aulaglobal`.

<details>
<summary><strong>Opciones del programa de configuración</strong></summary>

| Opción | Efecto |
|---|---|
| `--only-clients` | Configura solo los asistentes, sin repetir los pasos de la cuenta. |
| `--clients claude-code,codex` | Configura solo los asistentes indicados. |
| `--yes` | Acepta los cambios propuestos sin preguntar. |
| `--help` | Muestra la ayuda y los nombres de los asistentes disponibles. |

Ejemplo: `aulaglobal-mcp setup --only-clients --clients claude-code --yes`

</details>

---

## Uso

Pídele las cosas a tu asistente con tus propias palabras. Para comprobar que todo
funciona, prueba con:

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

Si vuelves a descargar, el programa solo baja los archivos nuevos o modificados.

---

## Actualización

Para instalar la última versión, abre una terminal y ejecuta:

```sh
aulaglobal-mcp update
```

Si instalaste el programa a mano y no está en el PATH, ejecútalo desde su carpeta
(`./aulaglobal-mcp update` o `.\aulaglobal-mcp.exe update` en Windows).

El programa muestra la versión instalada y la nueva, te pide confirmación y se
actualiza solo. Antes de instalar, comprueba la firma digital y la integridad de
la descarga; si algo falla, no cambia nada. Cuando termine, reinicia el
asistente.

- `aulaglobal-mcp update --check` solo comprueba si hay una versión nueva, sin
  instalarla.
- Una vez al día como máximo, el programa comprueba si hay una versión nueva; si
  la hay, el asistente te lo dirá. El asistente nunca actualiza por su cuenta.
  La actualización siempre la haces tú desde la terminal.
- Para desactivar esa comprobación, añade la variable de entorno
  `AULAGLOBAL_UPDATE_CHECK=0` a la configuración del servidor en tu asistente.

---

## Configuración manual de los asistentes

Solo hace falta si el programa de configuración no detecta tu asistente. En los
ejemplos, sustituye `/ruta/al/aulaglobal-mcp` por la ruta completa del programa.
En Windows termina en `aulaglobal-mcp.exe` y puedes usar barras `/`, por
ejemplo `C:/Users/Ana/AppData/Local/Programs/aulaglobal-mcp/aulaglobal-mcp.exe`.

<details>
<summary><strong>Claude Code</strong></summary>

```sh
claude mcp add --scope user aulaglobal -- "/ruta/al/aulaglobal-mcp" --transport stdio
```

Si solo lo quieres para un proyecto, crea un archivo `.mcp.json` en su carpeta:

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
`aulaglobal` del ejemplo de Claude Code dentro de `mcpServers`, sin borrar lo
demás. El archivo está en:

| Sistema | Ruta |
|---|---|
| Windows | `%APPDATA%\Claude\claude_desktop_config.json` |
| macOS | `~/Library/Application Support/Claude/claude_desktop_config.json` |
| Linux | `~/.config/Claude/claude_desktop_config.json` |

Reinicia la aplicación después de guardar.

</details>

<details>
<summary><strong>Codex y ChatGPT (aplicación de escritorio)</strong></summary>

Los dos usan la misma configuración. Añade esto a `~/.codex/config.toml`
(en Windows, `%USERPROFILE%\.codex\config.toml`):

```toml
[mcp_servers.aulaglobal]
command = "/ruta/al/aulaglobal-mcp"
args = ["--transport", "stdio"]
```

Reinicia la aplicación. Esta configuración no sirve para ChatGPT en el
navegador ([más información](docs/TECNICO.md#chatgpt-en-la-web)).

</details>

<details>
<summary><strong>OpenCode</strong></summary>

Si tu versión lo admite:

```sh
opencode mcp add aulaglobal --global -- /ruta/al/aulaglobal-mcp --transport stdio
```

Si no, edita `~/.config/opencode/opencode.json` (o `opencode.jsonc`; la ruta es
la misma en Windows y macOS) y añade la entrada dentro de `mcp.servers`, sin
borrar los demás servidores:

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

El archivo está en `~/.gemini/config/mcp_config.json` (en Windows,
`%USERPROFILE%\.gemini\config\mcp_config.json`). Para aplicar el cambio, usa
**Restart** en **Customizations → Installed → MCP Servers**, o recarga desde
`/mcp` en el terminal.

</details>

Si en la configuración elegiste carpetas distintas de las propuestas, usa el
bloque que muestra el programa al terminar, porque ya incluye esas rutas.

---

## Problemas frecuentes

| Mensaje o síntoma | Qué hacer |
|---|---|
| `aulaglobal-mcp: command not found` o «no se reconoce como comando» | Abre una terminal nueva para que se aplique el PATH. Si sigue sin funcionar, ejecuta el programa con su ruta completa. |
| El instalador dice que falta `ssh-keygen` | Sigue las instrucciones que muestra o usa la [instalación manual](#opción-b-instalación-manual). En Windows, se activa en **Configuración → Aplicaciones → Características opcionales → Cliente OpenSSH**. |
| «La sesión de AulaGlobal ha caducado…» | AulaGlobal caduca las claves de acceso cada cierto tiempo. Inicia sesión en AulaGlobal en el navegador y ejecuta `aulaglobal-mcp setup`, que te pedirá una cookie nueva. No hace falta reiniciar el asistente. |
| «El token de AulaGlobal es válido, pero Moodle ha denegado el acceso…» | La clave no ha caducado. AulaGlobal no permite esa función con tu cuenta, así que no hace falta renovar nada. |
| `cannot read token file` | Ejecuta `aulaglobal-mcp setup` de nuevo. Si usaste otras carpetas, revisa la configuración manual. |
| El asistente no muestra `aulaglobal` | Reinicia el asistente. Si sigue sin aparecer, ejecuta `aulaglobal-mcp setup --only-clients`. |
| No se puede obtener la clave de acceso | Cierra sesión en AulaGlobal, vuelve a entrar y repite el paso con una cookie nueva. |
| Un archivo no se puede leer | El programa no hace OCR, así que no lee PDF escaneados. Tampoco lee archivos con contraseña ni los formatos antiguos `.doc`, `.ppt` y `.xls`. Guarda una copia en `.docx`, `.pptx`, `.xlsx` o PDF con texto. Si un archivo muy largo tarda, repite la petición unos segundos después. |

---

## Seguridad y privacidad

**Qué hace este programa con tus datos**

- **El programa no usa tu contraseña.** Obtiene el acceso a partir de tu sesión
  del navegador.
- **La clave de acceso se queda en tu equipo**, en la carpeta de configuración
  de tu usuario y con permisos restringidos. El asistente de IA nunca la recibe.
- **Sin intermediarios.** El programa se conecta directamente a AulaGlobal desde
  tu equipo. El autor del proyecto no recibe tus documentos, tus credenciales
  ni ningún otro dato. La única conexión adicional es la comprobación diaria de
  versiones nuevas en GitHub, que puedes desactivar (ver
  [Actualización](#actualización)).
- **Datos personales mínimos.** Al comprobar la conexión, el asistente no recibe
  tu nombre ni tu identificador de usuario. El programa solo consulta las
  calificaciones y el estado de las entregas si lo pides.
- **Uso moderado de AulaGlobal.** El programa limita el número de peticiones por
  segundo, reutiliza durante unos segundos las respuestas recientes y, si
  AulaGlobal pide esperar, se detiene el tiempo indicado.
- **Descargas verificadas.** El instalador y el comando `update` comprueban la
  firma digital de cada versión antes de instalarla.

**Qué hace el asistente con los resultados**

- **El asistente recibe lo que devuelven las herramientas**: listas de
  asignaturas, textos de tus apuntes, enunciados de entregas, etc. Según el
  asistente que uses, ese contenido llega a su proveedor de IA (Anthropic,
  OpenAI, Google…), que lo trata según sus condiciones y su política de
  privacidad. Revísalas antes de usarlo con materiales que no quieras compartir.
- **Los límites de carpeta solo afectan a este programa.** Las herramientas de
  AulaGlobal MCP solo pueden leer y escribir dentro de la carpeta `AulaGlobal`,
  pero el asistente puede tener sus propios permisos (por ejemplo, para leer
  otros archivos o ejecutar comandos), que dependen de cómo lo hayas
  configurado.

---

## Condiciones de uso

Al usar este programa aceptas lo siguiente:

1. **Proyecto independiente.** No está desarrollado, respaldado ni supervisado
   por la Universidad Carlos III de Madrid.
2. **Solo con acceso autorizado.** Úsalo únicamente con tu propia cuenta de
   AulaGlobal, para contenido al que ya tienes acceso, y respeta la normativa
   de la universidad.
3. **Tú decides qué compartes.** Eres responsable de comprobar que puedes
   compartir con el asistente y con su proveedor de IA el contenido que le
   pides procesar. Ten en cuenta los derechos de autor de los materiales y los
   datos personales que puedan contener.
4. **Sin garantías.** El software se ofrece tal cual, sin garantía de ningún
   tipo. En la medida en que lo permita la ley, los autores no son responsables
   de los daños o perjuicios derivados de su uso, como se indica en la
   [licencia](LICENSE).

---

## Desinstalación

Abre una terminal y ejecuta:

```sh
aulaglobal-mcp uninstall
```

El programa te pregunta qué quieres hacer:

| Opción | Qué hace |
|---|---|
| 1. Desconectar AulaGlobal de algunos asistentes | Te deja elegir de qué asistentes quitarlo. El programa sigue instalado. |
| 2. Cerrar la sesión de AulaGlobal | Borra la clave de acceso de tu equipo. Para volver a usarlo, ejecuta `aulaglobal-mcp setup`. |
| 3. Desinstalar todo | Desconecta todos los asistentes y borra la clave, el programa y la línea que el instalador añadió al PATH. |

Antes de cambiar nada, muestra la lista exacta de lo que va a hacer y te pide
confirmación. El programa no borra tus materiales descargados salvo que lo
indiques. La opción 3 lo pregunta aparte, y la respuesta por defecto es «No».

Con `aulaglobal-mcp uninstall --dry-run` puedes ver lo que se haría sin cambiar
nada. Las demás opciones se muestran con `aulaglobal-mcp uninstall --help`.

<details>
<summary><strong>Desinstalar a mano</strong> (por ejemplo, si ya borraste el programa)</summary>

1. Desconecta el programa de tus asistentes. En Claude Code:
   `claude mcp remove aulaglobal --scope user`. En los demás, borra la entrada
   `aulaglobal` de su configuración (ver
   [Configuración manual de los asistentes](#configuración-manual-de-los-asistentes)).
2. Borra el programa de la carpeta donde lo instalaste.
3. Borra la clave de acceso:
   - Windows: `%APPDATA%\aulaglobal-mcp`
   - macOS: `~/Library/Application Support/aulaglobal-mcp`
   - Linux: `~/.config/aulaglobal-mcp`
4. Si el instalador añadió la carpeta al PATH, quita la línea marcada con
   `# aulaglobal-mcp` de tu perfil de terminal (`~/.zshrc`, `~/.bashrc` o
   similar). En Windows, quita la carpeta de la variable PATH de tu usuario.
5. Si ya no quieres los materiales descargados, borra la carpeta `AulaGlobal`.

</details>

---

## Agradecimientos

Este proyecto parte de
[AGDownloader](https://github.com/Astrak00/AGDownloader), de
[Eduardo Alarcón (Astrak00)](https://github.com/Astrak00), un descargador de
AulaGlobal para terminal publicado con licencia MIT. De él vienen el método para
obtener el acceso a partir de la sesión del navegador, la línea para la consola y
el tratamiento de los nombres de las asignaturas.

## Licencia

Distribuido bajo la [licencia MIT](LICENSE). El software se proporciona tal
cual, sin garantía de ningún tipo.

El programa incluye partes basadas en AGDownloader y bibliotecas de terceros,
cada una con su propia licencia. Sus avisos y textos de licencia están en
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md), que también se incluye en cada
versión publicada.

## Documentación técnica

[docs/TECNICO.md](docs/TECNICO.md) explica el modo servidor HTTP con Docker, las
opciones de configuración, el modelo de seguridad, los instaladores, las
actualizaciones y el proceso de desarrollo y publicación.
