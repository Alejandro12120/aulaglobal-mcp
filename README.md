# AulaGlobal para tu asistente de IA

Este programa permite que tu asistente de IA (Claude, Codex, ChatGPT u OpenCode) entre en
tu [AulaGlobal](https://aulaglobal.uc3m.es) de la UC3M y te ayude con tus
asignaturas. Puedes pedirle cosas como:

> «¿Qué entregas tengo esta semana?»
>
> «Descárgate los apuntes de Sistemas Operativos y resúmeme el tema 3.»
>
> «Busca en mis apuntes dónde explican el algoritmo de Dijkstra.»

Los apuntes se guardan en tu ordenador, en la carpeta `AulaGlobal` de tu usuario. Tu contraseña no
se usa nunca y tu clave de acceso se queda guardada en tu ordenador, sin que el
asistente la vea.

> **Proyecto no oficial**, sin relación con la Universidad Carlos III de Madrid.
> Úsalo solo con tu propia cuenta y respetando la normativa de la universidad y
> los derechos de autor de los materiales.

---

## Antes de empezar

Necesitas un ordenador con Windows, Mac o Linux y un asistente compatible:
Claude Code, Claude Desktop, Codex, ChatGPT de escritorio u OpenCode.

## 1. Descarga el programa para tu sistema

Abre la [página de descargas](https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest)
y elige el archivo de tu ordenador:

| Tu ordenador | Archivo que termina en… |
|---|---|
| Windows habitual | `windows_x64.zip` |
| Windows con ARM | `windows_arm64.zip` |
| Mac con chip Apple (M1–M4) | `macos_apple-silicon.tar.gz` |
| Mac Intel | `macos_intel.tar.gz` |
| Linux habitual | `linux_x64.tar.gz` |
| Linux con ARM | `linux_arm64.tar.gz` |

Para saber qué Mac tienes: **menú Apple → Acerca de este Mac**.
Descomprime el archivo y guarda el programa en una carpeta que no vayas a mover
después. No necesitas instalar Go, Docker, WSL ni bash.

## 2. Conecta tu cuenta de AulaGlobal

Abre una terminal en esa carpeta y escribe:

**Windows (PowerShell):**

```powershell
.\aulaglobal-mcp.exe setup
```

También puedes abrir el programa con doble clic en Windows.

**Mac y Linux:**

```sh
./aulaglobal-mcp setup
```

Si dice que no tienes permiso, escribe primero `chmod +x aulaglobal-mcp`.

El programa te guía: abre AulaGlobal, entra con tu cuenta y sigue las
instrucciones para copiar un dato de tu sesión (la *cookie*). Pégalo solo cuando
el programa lo pida. No se ve mientras escribes. Tu contraseña no hace falta.
Al terminar verás **«Token válido. Hola, tu nombre»** si ha podido comprobarlo.

> Ese dato da acceso a tu cuenta. No lo compartas con nadie, tampoco con el
> asistente de IA: pégalo solo en el programa cuando te lo pida.
<details>
<summary>El ordenador avisa de que no conoce el programa</summary>

Todavía no tiene firma digital. Descárgalo solo desde la página del proyecto.

En Windows: **Más información → Ejecutar de todas formas**.

En Mac: prueba **clic derecho → Abrir**. Si sigue bloqueado, puedes quitar la
marca de descarga desde la terminal, en la carpeta del programa:

```sh
xattr -d com.apple.quarantine aulaglobal-mcp
```

</details>

## 3. setup lo añade por ti

El programa busca Claude Code, Claude Desktop, Codex/ChatGPT de escritorio y OpenCode.
setup añade AulaGlobal a los que tengas instalados. Te pregunta antes
de añadirlo a cada uno. Responde `s` si quieres; después reinicia el asistente.
En Claude Desktop, Codex y OpenCode guarda una copia con el nombre terminado en `.bak`.
En Claude Code pide permiso antes de reemplazar un registro de usuario.
Abre una sesión nueva y escribe `/mcp` para comprobarlo.

Si no encuentra tu asistente, puedes hacerlo a mano. Donde pone
`/ruta/al/aulaglobal-mcp`, usa la ruta completa del programa que descargaste.
En Windows termina en `aulaglobal-mcp.exe`.

<details>
<summary>Claude Code: configuración manual</summary>

Ejecuta el comando que imprime setup, por ejemplo:

```sh
claude mcp add --scope user aulaglobal -- "/ruta/al/aulaglobal-mcp" --transport stdio
```

También puedes guardar este contenido en `.mcp.json`, en tu proyecto:

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

En Windows, escribe las barras de las rutas como `/` (por ejemplo,
`C:/Users/Ana/Programas/aulaglobal-mcp.exe`).
Si elegiste otras carpetas en setup, usa el comando que imprime al final
si falla la conexión automática.

</details>

<details>
<summary>Claude Desktop: configuración manual</summary>

Ve a **Ajustes → Desarrollador → Editar configuración** y añade el bloque
`aulaglobal` del ejemplo anterior dentro de `mcpServers`, conservando los demás.
El archivo está en:

- Windows: `%APPDATA%\Claude\claude_desktop_config.json`.
- Mac: `~/Library/Application Support/Claude/claude_desktop_config.json`.
- Linux: `~/.config/Claude/claude_desktop_config.json`.

</details>

<details>
<summary>Codex / ChatGPT de escritorio: configuración manual</summary>

Abre `~/.codex/config.toml` (en Windows, `%USERPROFILE%\.codex\config.toml`)
y añade:

```toml
[mcp_servers.aulaglobal]
command = "/ruta/al/aulaglobal-mcp"
args = ["--transport", "stdio"]
```

Usa barras `/` en Windows. Después reinicia la aplicación.
Esto es para ChatGPT de escritorio; la web se conecta de otra manera
([detalles técnicos](docs/TECNICO.md#chatgpt-en-la-web)).

</details>

Escribe `/mcp` en tu asistente para comprobar que aparece `aulaglobal`.

## 4. ¡A usarlo!

Pídele las cosas con tus palabras. Para empezar, prueba:

- «Comprueba mi conexión con AulaGlobal.» (debería decir tu nombre)
- «¿Qué asignaturas tengo?»
- «¿Qué hay subido en *Cálculo*?»
- «Descárgate los PDF de *Cálculo* y explícame el primero.»
- «¿Qué entregas tengo pendientes? ¿Cuáles he entregado ya?»

Lo que descargues queda en la carpeta `AulaGlobal` de tu usuario, ordenado por asignatura y tema:

```
AulaGlobal/
└── Cálculo/
    ├── Tema 1/
    │   └── apuntes.pdf
    └── Entregas/
        └── Práctica 1/
            └── enunciado.pdf
```

Si vuelves a pedirle que descargue, solo baja lo nuevo o lo que el profesor haya
cambiado.

---

## Si algo falla

**Dice «token invalid or expired».**
Tu acceso ha caducado (AulaGlobal lo hace cada cierto tiempo). Vuelve a
ejecutar el comando del paso 2 y responde `s` cuando pregunte si quieres
reemplazarlo.

**Dice «cannot read token file».**
Vuelve a ejecutar el paso 2. Si elegiste otra carpeta, usa la configuración
que imprime setup al terminar.

**El asistente no encuentra `aulaglobal`.**
Revisa las rutas del paso 3 y reinicia el asistente.

**No puede leer un PDF.**
Si el PDF es un escaneo (fotos de páginas), no tiene texto que leer. Si es muy
largo, puede tardar: pídeselo otra vez pasados unos segundos.

**`setup` dice que no ha podido obtener el token.**
Cierra sesión en AulaGlobal, vuelve a entrar y repite el paso 2 con un dato
nuevo.

---

## ¿Es seguro?

- Tu clave se guarda en la carpeta privada de configuración de tu usuario
  (el programa te indica las rutas). No la compartas ni la subas a internet.
- El asistente no ve nunca esa clave: el programa la usa por dentro.
- El programa solo guarda y lee archivos dentro de la carpeta `AulaGlobal` de tu usuario.
- Todo funciona en tu ordenador: no hay servidores intermedios.

---

## Agradecimientos

Este proyecto se basa en
**[AGDownloader](https://github.com/Astrak00/AGDownloader)**, de
[Eduardo Alarcón (Astrak00)](https://github.com/Astrak00), un descargador de
AulaGlobal para la terminal publicado con licencia MIT. De él vienen la idea de
obtener el acceso a partir de la sesión del navegador, la línea para la consola y
la forma de limpiar los nombres de las asignaturas. ¡Gracias por compartirlo!

---

## Licencia

Publicado con licencia [MIT](LICENSE). El programa se ofrece **tal cual, sin
ninguna garantía**: úsalo bajo tu propia responsabilidad.

---

## Para usuarios avanzados

En [docs/TECNICO.md](docs/TECNICO.md) están el modo servidor HTTP con Docker,
todas las opciones de configuración, los detalles de seguridad y cómo
desarrollar y probar el proyecto.

<details>
<summary>OpenCode: configuración manual</summary>

setup lo añade por ti en OpenCode V1. Si no puede hacerlo, te muestra un
bloque para copiar sin tocar tu configuración. Abre `opencode.json` o
`opencode.jsonc` en la carpeta `.config/opencode` de tu usuario
(también en Windows y Mac). Si ya tiene contenido, añade AulaGlobal dentro
de `mcp`, conservando los demás servidores:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "aulaglobal": {
      "type": "local",
      "command": ["/ruta/completa/aulaglobal-mcp", "--transport", "stdio"],
      "enabled": true
    }
  }
}
```

Cambia la ruta por la del programa descargado. En Windows usa barras `/`
o duplica cada barra `\\` de la ruta. Si elegiste otras carpetas, usa el
bloque que imprime setup. No pegues tu cookie ni tu token aquí.

En OpenCode V2 puedes ejecutar `opencode mcp add aulaglobal --global -- /ruta/completa/aulaglobal-mcp --transport stdio`.
Su configuración coloca los servidores dentro de `mcp.servers` y no usa `enabled`.
Reinicia OpenCode o abre una sesión nueva después de añadirlo.

</details>
