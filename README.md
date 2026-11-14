# AulaGlobal para tu asistente de IA

Este programa permite que tu asistente de IA (Claude, Codex o ChatGPT) entre en
tu [AulaGlobal](https://aulaglobal.uc3m.es) de la UC3M y te ayude con tus
asignaturas. Puedes pedirle cosas como:

> «¿Qué entregas tengo esta semana?»
>
> «Descárgate los apuntes de Sistemas Operativos y resúmeme el tema 3.»
>
> «Busca en mis apuntes dónde explican el algoritmo de Dijkstra.»

Los apuntes se guardan en tu ordenador, en la carpeta `data`. Tu contraseña no
se usa nunca y tu clave de acceso se queda guardada en tu ordenador, sin que el
asistente la vea.

---

## Antes de empezar

Necesitas tres cosas:

1. **Un ordenador con Linux o Mac.** En Windows funciona dentro de
   [WSL](https://learn.microsoft.com/es-es/windows/wsl/install).
2. **[Docker](https://docs.docker.com/get-docker/)** instalado. Solo hace falta
   la primera vez, para preparar el programa.
3. **Un asistente** compatible: [Claude Code](https://claude.com/claude-code),
   Claude Desktop, Codex o la aplicación de escritorio de ChatGPT.

---

## 1. Descarga el proyecto

Abre una terminal y escribe:

```bash
git clone <url-de-este-repositorio> aulaglobal-mcp
cd aulaglobal-mcp
```

## 2. Conecta tu cuenta de AulaGlobal

En la misma terminal, escribe:

```bash
./obtener-token.sh
```

El programa te va guiando paso a paso. La primera vez te preguntará si quieres
prepararlo con Docker: responde `s`.

Después te pedirá un dato de tu sesión de AulaGlobal (la *cookie*). Para
conseguirlo:

1. Entra en <https://aulaglobal.uc3m.es> con tu usuario, como siempre.
2. Pulsa **F12** (en Mac, `Cmd + Option + I`). Se abre un panel lateral.
3. Haz clic en la pestaña **Consola**.
4. Copia esta línea, pégala ahí y pulsa **Enter**:

   ```js
   copy(("; " + document.cookie).split("; MoodleSessionag=").pop().split(";").shift())
   ```

   No verás nada especial (como mucho la palabra `undefined`). El dato ya está
   copiado.
5. Vuelve a la terminal, pégalo y pulsa **Enter**. Por seguridad, lo que pegas
   no se ve en pantalla.

Si todo ha ido bien, verás **«Token válido. Hola, *tu nombre*»**. Listo.

<details>
<summary>¿Chrome no te deja pegar en la consola?</summary>

La primera vez, Chrome lo bloquea por seguridad. Escribe `allow pasting` (o
`permitir pegar`), pulsa Enter y vuelve a pegar la línea.

</details>

<details>
<summary>¿No funciona la línea de la consola?</summary>

Puedes copiar el dato a mano. En el mismo panel, ve a **Aplicación → Cookies →
https://aulaglobal.uc3m.es** (en Firefox: **Almacenamiento → Cookies**), busca la
fila `MoodleSessionag` y copia lo que pone en **Valor**.

</details>

> ⚠️ Ese dato da acceso a tu cuenta. No se lo pases a nadie, ni tampoco al
> asistente de IA: pégalo solo en la terminal cuando el programa te lo pida.

## 3. Añádelo a tu asistente

En todos los casos tienes que poner la ruta de la carpeta del proyecto. Para
saberla, escribe `pwd` en la terminal dentro de la carpeta. Te saldrá algo como
`/home/ana/aulaglobal-mcp`; úsalo donde pone `/ruta/a/aulaglobal-mcp`.

### Claude Code

Crea un archivo llamado `.mcp.json` dentro de la carpeta del proyecto con este
contenido (cambia las rutas):

```json
{
  "mcpServers": {
    "aulaglobal": {
      "command": "/ruta/a/aulaglobal-mcp/aulaglobal-mcp",
      "args": ["--transport", "stdio"],
      "env": {
        "AULAGLOBAL_TOKEN_FILE": "/ruta/a/aulaglobal-mcp/secrets/token",
        "AULAGLOBAL_DOWNLOAD_DIR": "/ruta/a/aulaglobal-mcp/data"
      }
    }
  }
}
```

Abre Claude Code en esa carpeta (escribe `claude`) y acepta cuando te pregunte
por `aulaglobal`.

### Claude Desktop

Ve a **Ajustes → Desarrollador → Editar configuración**, pega el mismo contenido
de arriba (o añade el bloque `"aulaglobal"` si ya tenías otros) y reinicia la
aplicación.

### Codex y ChatGPT (aplicación de escritorio)

Los dos comparten la misma configuración, así que solo tienes que hacerlo una
vez. Escribe en la terminal (cambia las rutas):

```bash
codex mcp add aulaglobal \
  --env AULAGLOBAL_TOKEN_FILE=/ruta/a/aulaglobal-mcp/secrets/token \
  --env AULAGLOBAL_DOWNLOAD_DIR=/ruta/a/aulaglobal-mcp/data \
  -- /ruta/a/aulaglobal-mcp/aulaglobal-mcp --transport stdio
```

Si no tienes Codex instalado, abre el archivo `~/.codex/config.toml` (créalo si no
existe) y añade esto:

```toml
[mcp_servers.aulaglobal]
command = "/ruta/a/aulaglobal-mcp/aulaglobal-mcp"
args = ["--transport", "stdio"]
env = { AULAGLOBAL_TOKEN_FILE = "/ruta/a/aulaglobal-mcp/secrets/token", AULAGLOBAL_DOWNLOAD_DIR = "/ruta/a/aulaglobal-mcp/data" }
```

Después reinicia Codex o ChatGPT. En ChatGPT puedes comprobarlo en **Settings →
MCP servers**; si no aparece, pulsa **Restart**.

> Esto funciona en la **aplicación de escritorio** de ChatGPT, no en la web.
> ([Documentación de OpenAI](https://learn.chatgpt.com/docs/extend/mcp?surface=app))

### Comprobar que funciona

Escribe `/mcp` en tu asistente: debería aparecer `aulaglobal` como conectado.

## 4. ¡A usarlo!

Pídele las cosas con tus palabras. Para empezar, prueba:

- «Comprueba mi conexión con AulaGlobal.» (debería decir tu nombre)
- «¿Qué asignaturas tengo?»
- «¿Qué hay subido en *Cálculo*?»
- «Descárgate los PDF de *Cálculo* y explícame el primero.»
- «¿Qué entregas tengo pendientes? ¿Cuáles he entregado ya?»

Lo que descargues queda en la carpeta `data`, ordenado por asignatura y tema:

```
data/
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
ejecutar `./obtener-token.sh` y responde `s` cuando pregunte si quieres
reemplazarlo.

**Dice «cannot read token file».**
Falta el paso 2, o la ruta que pusiste en el paso 3 está mal. Revisa que empiece
por `/` y que coincida con lo que te dice `pwd`.

**El asistente no encuentra `aulaglobal`.**
Revisa las rutas del paso 3 y reinicia el asistente.

**No puede leer un PDF.**
Si el PDF es un escaneo (fotos de páginas), no tiene texto que leer. Si es muy
largo, puede tardar: pídeselo otra vez pasados unos segundos.

**`obtener-token.sh` dice que no ha podido obtener el token.**
Cierra sesión en AulaGlobal, vuelve a entrar y repite el paso 2 con un dato
nuevo.

---

## ¿Es seguro?

- Tu clave de acceso se guarda en la carpeta `secrets`, y solo tu usuario puede
  leerla. No la compartas ni la subas a internet.
- El asistente no ve nunca esa clave: el programa la usa por dentro.
- El programa solo guarda y lee archivos dentro de la carpeta `data`.
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

## Para usuarios avanzados

En [docs/TECNICO.md](docs/TECNICO.md) están el modo servidor HTTP con Docker,
todas las opciones de configuración, los detalles de seguridad y cómo
desarrollar y probar el proyecto.
