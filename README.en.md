<div align="center">

# AulaGlobal MCP

**Connect your AI assistant to AulaGlobal (UC3M)**

[Español](README.md) · English

[Installation](#installation) · [Usage](#usage) · [Updating](#updating) · [Troubleshooting](#troubleshooting) · [Security and privacy](#security-and-privacy) · [Terms of use](#terms-of-use) · [Uninstalling](#uninstalling)

</div>

---

AulaGlobal MCP lets your AI assistant browse your
[AulaGlobal](https://aulaglobal.uc3m.es) courses, download their materials and
work with them. It works with Claude Code, Claude Desktop, Codex, ChatGPT
(desktop app), OpenCode and Antigravity.

| Feature | What it does |
|---|---|
| Courses | Lists your current, past or upcoming courses. |
| Materials | Shows and downloads each course's files, organised by section. |
| Reading and search | Reads PDF, Word, PowerPoint and text files, and searches everything downloaded. |
| Assignments | Shows due dates, instructions and, if you ask, submission status and grade. |

Some examples of what you can ask:

> Which assignments are due this week?
>
> Download the Operating Systems notes and summarise unit 3.
>
> Find where Dijkstra's algorithm is explained in my notes.

The program's own messages (installer, setup and tool output) are in Spanish.
Your assistant can answer you in English.

> [!IMPORTANT]
> **Unofficial project**, not affiliated with Universidad Carlos III de Madrid.
> Use it only with your own account, in line with the university's rules and
> the copyright of the course materials.

---

## Installation

You need a computer running Windows, macOS or Linux and one of the supported
assistants. Nothing else needs to be installed.

There are two ways to install it. The **automatic installation** is the simplest
and is recommended in most cases.

### Option A: automatic installation (recommended)

Open a terminal, copy the command for your system, paste it and press **Enter**.

**macOS and Linux** (the **Terminal** app):

```sh
curl -fsSL https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest/download/install.sh | sh
```

**Windows** (search for **PowerShell** in the Start menu and open it):

```powershell
irm https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest/download/install.ps1 | iex
```

The installer does the following, asking for confirmation where needed:

1. Detects your system and downloads the latest version.
2. Checks the digital signature and integrity of the download. If anything does
   not match, it stops without installing anything.
3. Asks where to install the program and suggests a folder. Press **Enter** to
   accept it:
   - macOS and Linux: `~/.local/bin`
   - Windows: `%LOCALAPPDATA%\Programs\aulaglobal-mcp`
4. Offers to add that folder to your PATH, so you can run `aulaglobal-mcp` from
   any terminal.
5. Opens the [setup program](#setup), which connects your account and your
   assistants.

If the program is already installed, the installer detects it and replaces it
with the new version.

<details>
<summary><strong>Advanced options</strong></summary>

The installer reads these environment variables:

| Variable | Effect |
|---|---|
| `AULAGLOBAL_VERSION=v0.1.5` | Installs that version instead of the latest. |
| `AULAGLOBAL_INSTALL_DIR=/path` | Installs into that folder without asking. |
| `AULAGLOBAL_YES=1` | Accepts every suggested option without asking. |

Example on macOS and Linux:

```sh
curl -fsSL https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest/download/install.sh | AULAGLOBAL_VERSION=v0.1.5 sh
```

Example on Windows:

```powershell
$env:AULAGLOBAL_VERSION = "v0.1.5"; irm https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest/download/install.ps1 | iex
```

The signature is checked with `ssh-keygen`, which ships with macOS, most Linux
distributions and Windows 10 and 11. If your computer does not have it, the
installer explains how to add it; you can also use the manual installation.

</details>

### Option B: manual installation

If you prefer not to run a script, you can download the program yourself.

**1. Download** the file for your computer from the
[releases page](https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest):

| Computer | File |
|---|---|
| Windows (Intel/AMD) | `…_windows_x64.zip` |
| Windows (ARM) | `…_windows_arm64.zip` |
| Mac with Apple chip (M1 or later) | `…_macos_apple-silicon.tar.gz` |
| Mac with Intel processor | `…_macos_intel.tar.gz` |
| Linux (x86-64) | `…_linux_x64.tar.gz` |
| Linux (ARM) | `…_linux_arm64.tar.gz` |

On a Mac, the processor type is shown under **Apple menu → About This Mac**.

**2. Extract** the file and keep the program in a folder where it will stay; your
assistants will look for the program in that location.

**3. Open a terminal in that folder** and run the setup program:

| System | Command |
|---|---|
| Windows (PowerShell) | `.\aulaglobal-mcp.exe setup` |
| macOS and Linux | `./aulaglobal-mcp setup` |

On Windows you can also double-click it. On macOS and Linux, if you get a
permissions error, first run `chmod +x aulaglobal-mcp`.

<details>
<summary><strong>If the system shows a warning when opening the program</strong></summary>

The program is not yet signed by Apple or Microsoft, so when you download it with
a browser the system may show a warning the first time. Only download it from
the project's official page. The automatic installation does not trigger this
warning.

- **Windows:** select **More info → Run anyway**.
- **macOS:** right-click the program and choose **Open**. If it is still
  blocked, run this in the program's folder:

  ```sh
  xattr -d com.apple.quarantine aulaglobal-mcp
  ```

</details>

### Setup

The setup program opens automatically at the end of the automatic installation.
You can open it again at any time with `aulaglobal-mcp setup`. It has four
steps:

1. **Folders.** Shows where the access key and the materials will be stored.
2. **AulaGlobal account.** Asks for your browser's session cookie and turns it
   into an access key. Instructions are shown on screen.
   When it can, setup copies the command to the clipboard for you. Once it
   has the key, it clears the clipboard if it still holds the cookie.
3. **Check.** Verifies the key and shows the account name.
4. **Assistants.** Detects the installed assistants and offers to connect them.

<details>
<summary><strong>How to get the session cookie</strong> (the setup program walks you through this step; these instructions are for reference only)</summary>

1. Sign in at <https://aulaglobal.uc3m.es>.
2. Open the developer tools with **F12** (on a Mac, **Cmd + Option + I**) and
   select the **Console** tab.
3. Paste this line and press **Enter**. The value is copied to your clipboard:

   ```js
   copy(("; " + document.cookie).split("; MoodleSessionag=").pop().split(";").shift())
   ```

4. Go back to the terminal and paste it when asked. For security, the text is
   not shown while you paste it.

If Chrome does not let you paste into the console, type `allow pasting` and
press Enter. If the line does not work, you can also copy the value from
**Application → Cookies → https://aulaglobal.uc3m.es → `MoodleSessionag`**
(in Firefox, **Storage → Cookies**).

</details>

> [!WARNING]
> The cookie gives access to your account while the session is open. Do not
> share it or paste it into the assistant's chat: enter it only in the setup
> program.

In step 4, the program checks each assistant it finds:

| Situation | What it does |
|---|---|
| AulaGlobal is not connected | Asks whether to add it. |
| It is already connected correctly | Changes nothing. |
| There is a different configuration | Shows the difference and asks whether to update it. |
| The file cannot be changed safely | Shows how to do it by hand. |

Before changing a file, it saves a copy with the `.bak` extension. When it
finishes, it shows a summary and explains how to reload each assistant.

To check that it works, type `/mcp` in the assistant: `aulaglobal` should be
listed.

<details>
<summary><strong>Setup program options</strong></summary>

| Option | Effect |
|---|---|
| `--only-clients` | Configures only the assistants, without repeating the account steps. |
| `--clients claude-code,codex` | Configures only the listed assistants. |
| `--yes` | Accepts the proposed changes without asking. |
| `--help` | Shows the help and the names of the available assistants. |

Example: `aulaglobal-mcp setup --only-clients --clients claude-code --yes`

</details>

---

## Usage

Ask your assistant in your own words. To check that everything works, try:

1. "Check my connection to AulaGlobal."
2. "Which courses do I have this term?"
3. "What materials are there in Calculus?"
4. "Download the Calculus PDFs and summarise the first one."
5. "Which assignments are pending and which have I submitted?"

Materials are saved in the `AulaGlobal` folder in your user directory, organised
by course and section:

```text
AulaGlobal/
└── Cálculo/
    ├── Tema 1/
    │   └── apuntes.pdf
    └── Entregas/
        └── Práctica 1/
            └── enunciado.pdf
```

When you download again, only new or changed files are fetched.

---

## Updating

To install the latest version, open a terminal and run:

```sh
aulaglobal-mcp update
```

If you installed the program by hand and it is not on your PATH, run it from its
folder (`./aulaglobal-mcp update`, or `.\aulaglobal-mcp.exe update` on Windows).

The program shows the installed and the new version, asks for confirmation and
updates itself. Before installing, it checks the digital signature and the
integrity of the download; if anything fails, nothing is changed. When it
finishes, restart the assistant.

- `aulaglobal-mcp update --check` only checks whether a new version exists,
  without installing it.
- At most once a day, the program checks whether a new version is available; if
  there is one, the assistant will let you know. The assistant never updates on
  its own: you always run the update yourself from the terminal.
- To turn that check off, add the environment variable
  `AULAGLOBAL_UPDATE_CHECK=0` to the server configuration in your assistant.

---

## Manual assistant configuration

Only needed if the setup program does not detect your assistant. In the
examples, replace `/path/to/aulaglobal-mcp` with the full path to the program.
On Windows it ends in `aulaglobal-mcp.exe` and you can use forward slashes, for
example `C:/Users/Ana/AppData/Local/Programs/aulaglobal-mcp/aulaglobal-mcp.exe`.

<details>
<summary><strong>Claude Code</strong></summary>

```sh
claude mcp add --scope user aulaglobal -- "/path/to/aulaglobal-mcp" --transport stdio
```

If you only want it for one project, create a `.mcp.json` file in its folder:

```json
{
  "mcpServers": {
    "aulaglobal": {
      "command": "/path/to/aulaglobal-mcp",
      "args": ["--transport", "stdio"]
    }
  }
}
```

</details>

<details>
<summary><strong>Claude Desktop</strong></summary>

Open **Settings → Developer → Edit Config** and add the `aulaglobal` block from
the Claude Code example inside `mcpServers`, keeping everything else. The file
is located at:

| System | Path |
|---|---|
| Windows | `%APPDATA%\Claude\claude_desktop_config.json` |
| macOS | `~/Library/Application Support/Claude/claude_desktop_config.json` |
| Linux | `~/.config/Claude/claude_desktop_config.json` |

Restart the app after saving.

</details>

<details>
<summary><strong>Codex and ChatGPT (desktop app)</strong></summary>

Both share the same configuration. Add this to `~/.codex/config.toml`
(on Windows, `%USERPROFILE%\.codex\config.toml`):

```toml
[mcp_servers.aulaglobal]
command = "/path/to/aulaglobal-mcp"
args = ["--transport", "stdio"]
```

Restart the app. This configuration does not apply to ChatGPT in the browser
([more information, in Spanish](docs/TECNICO.md#chatgpt-en-la-web)).

</details>

<details>
<summary><strong>OpenCode</strong></summary>

If your version supports it:

```sh
opencode mcp add aulaglobal --global -- /path/to/aulaglobal-mcp --transport stdio
```

Otherwise, edit `~/.config/opencode/opencode.json` (or `opencode.jsonc`; the path
is the same on Windows and macOS) and add the entry inside `mcp.servers`,
keeping the other servers:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "aulaglobal": {
        "type": "local",
        "command": ["/path/to/aulaglobal-mcp", "--transport", "stdio"]
      }
    }
  }
}
```

In older versions (V1), the entry goes directly inside `mcp` and includes
`"enabled": true`. Restart OpenCode after the change.

</details>

<details>
<summary><strong>Antigravity</strong></summary>

In the agent panel, open **… → MCP Servers → Manage MCP Servers → View raw
config** and add the entry inside `mcpServers`:

```json
{
  "mcpServers": {
    "aulaglobal": {
      "command": "/path/to/aulaglobal-mcp",
      "args": ["--transport", "stdio"]
    }
  }
}
```

The file is at `~/.gemini/config/mcp_config.json` (on Windows,
`%USERPROFILE%\.gemini\config\mcp_config.json`). To apply the change, use
**Restart** under **Customizations → Installed → MCP Servers**, or reload from
`/mcp` in the terminal.

</details>

If you chose folders other than the suggested ones during setup, use the block
the program shows at the end, because it already includes those paths.

---

## Troubleshooting

The program's messages are in Spanish; the first column shows them as they
appear, with a translation where useful.

| Message or symptom | What to do |
|---|---|
| `aulaglobal-mcp: command not found` or "is not recognized as a command" | Open a new terminal so the PATH change takes effect. If it still fails, run the program with its full path. |
| The installer says `ssh-keygen` is missing | Follow the instructions it shows or use the [manual installation](#option-b-manual-installation). On Windows, enable it under **Settings → Apps → Optional features → OpenSSH Client**. |
| «La sesión de AulaGlobal ha caducado…» (the session has expired) | AulaGlobal expires access keys from time to time. Sign in to AulaGlobal in your browser and run `aulaglobal-mcp setup`: it will ask for a new cookie. You do not need to restart the assistant. |
| «El token de AulaGlobal es válido, pero Moodle ha denegado el acceso…» (the token is valid, but access was denied) | It has not expired: AulaGlobal does not allow that feature for your account. Nothing needs renewing. |
| `cannot read token file` | Run `aulaglobal-mcp setup` again. If you used other folders, check the manual configuration. |
| The assistant does not list `aulaglobal` | Restart the assistant. If it is still missing, run `aulaglobal-mcp setup --only-clients`. |
| The access key cannot be obtained | Sign out of AulaGlobal, sign in again and repeat the step with a new cookie. |
| A PDF cannot be read | Scanned PDFs contain no text. Very long ones can take a while: repeat the request a few seconds later. |

---

## Security and privacy

**What this program does with your data**

- **Your password is not used.** Access is obtained from your browser session.
- **The access key stays on your computer**, in your user's configuration
  folder with restricted permissions. The AI assistant never receives it.
- **No intermediaries.** The program connects directly to AulaGlobal from your
  computer. The project's author does not receive your documents, your
  credentials or any other data. The only other connection is the daily check
  for new versions on GitHub, which you can turn off (see
  [Updating](#updating)).
- **Minimal personal data.** When checking the connection, the assistant does
  not receive your name or user ID. Grades and submission status are only
  requested if you ask for them.
- **Light use of AulaGlobal.** The program limits the number of requests per
  second, reuses recent responses for a few seconds and, if AulaGlobal asks it
  to wait, pauses for the time requested.
- **Verified downloads.** The installer and the `update` command check each
  version's digital signature before installing it.

**What the assistant does with the results**

- **The assistant receives whatever the tools return**: course lists, the text
  of your notes, assignment instructions and so on. Depending on the assistant
  you use, that content is sent to its AI provider (Anthropic, OpenAI,
  Google…) and handled under its terms and privacy policy. Review them before
  using it with material you do not want to share.
- **Folder limits only apply to this program.** AulaGlobal MCP's tools can only
  read and write inside the `AulaGlobal` folder, but the assistant may have its
  own permissions (for example, to read other files or run commands), which
  depend on how you have configured it.

---

## Terms of use

By using this program you agree to the following:

1. **Independent project.** It is not developed, endorsed or supervised by
   Universidad Carlos III de Madrid.
2. **Authorised access only.** Use it only with your own AulaGlobal account and
   for content you already have access to, in line with the university's rules.
3. **You decide what you share.** You are responsible for checking that you may
   share the content you ask the assistant to process with the assistant and its
   AI provider, taking into account the copyright of the materials and any
   personal data they contain.
4. **No warranty.** The software is provided as is, without warranty of any
   kind. To the extent permitted by law, the authors are not liable for any
   damage arising from its use, as stated in the [licence](LICENSE).

---

## Uninstalling

Open a terminal and run:

```sh
aulaglobal-mcp uninstall
```

The program asks what you want to do:

| Option | What it does |
|---|---|
| 1. Disconnect AulaGlobal from some assistants | Lets you choose which assistants to remove it from. The program stays installed. |
| 2. Sign out of AulaGlobal | Deletes the access key from your computer. To use it again, run `aulaglobal-mcp setup`. |
| 3. Uninstall everything | Disconnects every assistant and deletes the access key, the program and the line the installer added to your PATH. |

Before changing anything, it shows exactly what it will do and asks for
confirmation. Your downloaded materials are not deleted unless you say so: in
option 3 it asks separately, and the default answer is "No".

`aulaglobal-mcp uninstall --dry-run` shows what would be done without changing
anything. The other options are listed by `aulaglobal-mcp uninstall --help`.

<details>
<summary><strong>Uninstalling by hand</strong> (for example, if you already deleted the program)</summary>

1. Disconnect the program from your assistants. In Claude Code:
   `claude mcp remove aulaglobal --scope user`. In the others, delete the
   `aulaglobal` entry from their configuration (see
   [Manual assistant configuration](#manual-assistant-configuration)).
2. Delete the program from the folder where you installed it.
3. Delete the access key:
   - Windows: `%APPDATA%\aulaglobal-mcp`
   - macOS: `~/Library/Application Support/aulaglobal-mcp`
   - Linux: `~/.config/aulaglobal-mcp`
4. If the installer added the folder to your PATH, remove the line marked
   `# aulaglobal-mcp` from your shell profile (`~/.zshrc`, `~/.bashrc` or
   similar). On Windows, remove the folder from your user PATH variable.
5. If you no longer want the downloaded materials, delete the `AulaGlobal`
   folder.

</details>

---

## Acknowledgements

This project builds on
[AGDownloader](https://github.com/Astrak00/AGDownloader) by
[Eduardo Alarcón (Astrak00)](https://github.com/Astrak00), a command-line
AulaGlobal downloader released under the MIT licence. The method for obtaining
access from the browser session, the console line and the handling of course
names come from it.

## Licence

Released under the [MIT licence](LICENSE). The software is provided
**as is, without warranty of any kind**.

The program includes parts based on AGDownloader and third-party libraries,
each under its own licence. Their notices and licence texts are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md), which is also included in every
release.

## Technical documentation

[docs/TECNICO.md](docs/TECNICO.md) (in Spanish) covers the HTTP server mode with
Docker, configuration options, the security model, the installers, updates, and
the development and release process.
