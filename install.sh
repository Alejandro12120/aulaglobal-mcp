#!/bin/sh
# Instalador de aulaglobal-mcp para macOS y Linux.
#
#   curl -fsSL https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest/download/install.sh | sh
#
# Variables de entorno opcionales. Con "curl | sh" ponlas delante de sh:
#   curl -fsSL <url> | AULAGLOBAL_VERSION=v0.1.5 sh
#
#   AULAGLOBAL_VERSION      instala esa versión (por ejemplo v0.1.5) en vez de la última
#   AULAGLOBAL_INSTALL_DIR  carpeta de instalación; si se define, no se pregunta
#   AULAGLOBAL_YES=1        acepta todas las opciones predeterminadas, sin preguntas
#
# Qué hace: descarga el archivo de tu sistema y checksums.txt con su firma,
# verifica la firma (ssh-keygen -Y verify, OpenSSH 8.1 o posterior) con la clave
# pública de abajo, comprueba el SHA-256 y solo entonces instala el programa.
# No existe ninguna forma de saltarse la verificación de la firma.
#
# Todo el programa está dentro de main(), que se llama en la última línea: si la
# descarga se corta a medias, el shell no llega a ejecutar nada.

set -eu

REPO='Alejandro12120/aulaglobal-mcp'
BIN_NAME='aulaglobal-mcp'
SIGN_NAMESPACE='aulaglobal-mcp-update'
# Misma clave que internal/selfupdate/update_key.pub (una prueba de Go lo comprueba).
SIGN_KEY='ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFNMn9RCnq08BKgWOIIpDJLaCTEpsREfzlK9VM5609tY aulaglobal-mcp-update'
MANUAL_URL="https://github.com/$REPO/releases/latest"

TMP=''
STAGED=''

cleanup() {
  [ -z "$STAGED" ] || rm -f "$STAGED" 2>/dev/null || true
  [ -z "$TMP" ] || rm -rf "$TMP" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

# ---------------------------------------------------------------- salida

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  BOLD=$(printf '\033[1m')
  RESET=$(printf '\033[0m')
else
  BOLD=''
  RESET=''
fi

ok() { printf '✓ %s\n' "$*"; }
note() { printf '· %s\n' "$*"; }
warn() { printf '! %s\n' "$*"; }
step() { printf '\n%s[%s/5] %s%s\n\n' "$BOLD" "$1" "$2" "$RESET"; }

# die MENSAJE [DETALLE...]: error y salida con código 1.
die() {
  first=$1
  shift
  printf '✗ %s\n' "$first" >&2
  for line in "$@"; do
    printf '  %s\n' "$line" >&2
  done
  exit 1
}

# ---------------------------------------------------------------- terminal

# Con "curl | sh" la entrada estándar es el propio script: las respuestas se
# leen de /dev/tty. Si no hay terminal (CI, cron...) se usan los valores
# predeterminados.
HAVE_TTY=0
if ( : </dev/tty ) 2>/dev/null; then
  HAVE_TTY=1
fi
INTERACTIVE=0
if [ "$HAVE_TTY" = 1 ] && [ "${AULAGLOBAL_YES:-}" != 1 ]; then
  INTERACTIVE=1
fi

# ask TEXTO: deja la respuesta en REPLY.
ask() {
  REPLY=''
  printf '%s' "$1"
  IFS= read -r REPLY </dev/tty || REPLY=''
}

# confirm TEXTO PREDETERMINADO(y|n): estado 0 si la respuesta es sí.
confirm() {
  if [ "$INTERACTIVE" != 1 ]; then
    [ "$2" = y ]
    return
  fi
  printf '\n'
  ask "$1"
  case $(printf '%s' "$REPLY" | tr '[:upper:]' '[:lower:]') in
    s | si | sí | sÍ | y | yes) return 0 ;;
    n | no) return 1 ;;
    *) [ "$2" = y ] ;;
  esac
}

# ---------------------------------------------------------------- utilidades

need() {
  command -v "$1" >/dev/null 2>&1 || die "Falta la herramienta «$1», necesaria para instalar." \
    "Instalación manual: $MANUAL_URL"
}

valid_tag() {
  printf '%s' "$1" | grep -Eq '^v[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9}$'
}

# download URL DESTINO
download() {
  if [ "$FETCH" = curl ]; then
    # CURL_SEC va sin comillas a propósito: son varias opciones.
    # shellcheck disable=SC2086
    curl -fsSL $CURL_SEC --retry 2 --connect-timeout 20 --max-time 900 -o "$2" "$1"
  else
    wget -q -T 30 -O "$2" "$1"
  fi
}

# expand_dir RUTA: expande ~, la hace absoluta y quita barras finales.
expand_dir() {
  d=$1
  # shellcheck disable=SC2088  # la ~ literal es justo lo que se compara
  case $d in
    '~') d=$HOME ;;
    '~/'*) d=$HOME/${d#'~/'} ;;
    '~'*) die "No se admite la ruta «$1»." "Escribe una ruta completa, por ejemplo $HOME/bin" ;;
  esac
  case $d in
    /*) ;;
    *) d=$PWD/$d ;;
  esac
  while [ "$d" != / ] && [ "${d%/}" != "$d" ]; do
    d=${d%/}
  done
  printf '%s' "$d"
}

# Los caracteres que dan problemas en la línea que se añade al perfil se rechazan.
check_dir_chars() {
  nl=$(printf '\n_')
  nl=${nl%_}
  case $1 in
    *\"* | *\$* | *\`* | *\\* | *:* | *"$nl"*)
      die "La carpeta «$1» contiene caracteres no admitidos (comillas, \$, \`, \\ o :)." \
        "Elige otra carpeta."
      ;;
  esac
}

# pretty_dir RUTA: muestra ~ en lugar de $HOME.
pretty_dir() {
  # shellcheck disable=SC2088  # se imprime una ~ literal a propósito
  case $1 in
    "$HOME") printf '~' ;;
    "$HOME"/*) printf '~/%s' "${1#"$HOME"/}" ;;
    *) printf '%s' "$1" ;;
  esac
}

# ---------------------------------------------------------------- paso 1

detect_platform() {
  os=$(uname -s 2>/dev/null || echo desconocido)
  arch=$(uname -m 2>/dev/null || echo desconocido)
  PLATFORM=''
  case $os in
    Darwin)
      case $arch in
        arm64)
          PLATFORM=macos_apple-silicon
          PLATFORM_NAME='macOS (Apple Silicon)'
          ;;
        x86_64)
          # Una terminal bajo Rosetta se presenta como x86_64 en un Mac Apple Silicon.
          if [ "$(sysctl -n hw.optional.arm64 2>/dev/null || echo 0)" = 1 ]; then
            PLATFORM=macos_apple-silicon
            PLATFORM_NAME='macOS (Apple Silicon)'
          else
            PLATFORM=macos_intel
            PLATFORM_NAME='macOS (Intel)'
          fi
          ;;
      esac
      ;;
    Linux)
      case $arch in
        x86_64 | amd64)
          PLATFORM=linux_x64
          PLATFORM_NAME='Linux (x64)'
          ;;
        aarch64 | arm64)
          PLATFORM=linux_arm64
          PLATFORM_NAME='Linux (ARM64)'
          ;;
      esac
      ;;
    MINGW* | MSYS* | CYGWIN*)
      die "Este instalador es para macOS y Linux." \
        "En Windows usa PowerShell: irm https://github.com/$REPO/releases/latest/download/install.ps1 | iex"
      ;;
  esac
  if [ -z "$PLATFORM" ]; then
    die "No hay versión publicada para este sistema ($os $arch)." \
      "Solo se publican macOS (Apple Silicon e Intel), Linux (x64 y ARM64) y Windows." \
      "Instalación manual: $MANUAL_URL"
  fi
}

resolve_latest() {
  TAG=''
  if [ "$FETCH" = curl ]; then
    # /releases/latest responde con una redirección a /releases/tag/vX.Y.Z.
    # shellcheck disable=SC2086
    loc=$(curl -fsS $CURL_SEC --connect-timeout 20 --max-time 60 -o /dev/null -w '%{redirect_url}' "$BASE_URL/latest" 2>/dev/null) || loc=''
    case $loc in
      */releases/tag/*) TAG=${loc##*/} ;;
    esac
  else
    # wget (GNU o busybox) no permite ver la redirección de forma portátil: la
    # versión se lee de los nombres de archivo del checksums.txt de la última
    # versión. Después todo se descarga por etiqueta y se verifica igual.
    sums=$(wget -q -T 30 -O - "$BASE_URL/latest/download/checksums.txt" 2>/dev/null | tr -d '\r') || sums=''
    ver=$(printf '%s\n' "$sums" | sed -n "s/^[0-9A-Fa-f]\{64\}[ *]*${BIN_NAME}_\([0-9]\{1,9\}\.[0-9]\{1,9\}\.[0-9]\{1,9\}\)_${PLATFORM}\.tar\.gz\$/\1/p" | head -n 1)
    [ -z "$ver" ] || TAG=v$ver
  fi
  if ! valid_tag "$TAG"; then
    die "No se pudo averiguar la última versión publicada." \
      "Comprueba tu conexión. Para elegir una versión concreta:" \
      "  curl -fsSL $MANUAL_URL/download/install.sh | AULAGLOBAL_VERSION=v0.1.5 sh" \
      "Instalación manual: $MANUAL_URL"
  fi
}

choose_version() {
  req=${AULAGLOBAL_VERSION:-}
  if [ -n "$req" ]; then
    case $req in
      v*) TAG=$req ;;
      *) TAG=v$req ;;
    esac
    valid_tag "$TAG" || die "La versión «$req» no es válida." \
      "Usa el formato vX.Y.Z, por ejemplo AULAGLOBAL_VERSION=v0.1.5"
    note "Versión elegida: $TAG"
  else
    resolve_latest
    note "Última versión publicada: $TAG"
  fi
  VERSION=${TAG#v}
  ARCHIVE="${BIN_NAME}_${VERSION}_${PLATFORM}.tar.gz"
}

choose_dir() {
  if [ -n "${AULAGLOBAL_INSTALL_DIR:-}" ]; then
    INSTALL_DIR=$(expand_dir "$AULAGLOBAL_INSTALL_DIR")
  else
    default_dir=$HOME/.local/bin
    INSTALL_DIR=$default_dir
    if [ "$INTERACTIVE" = 1 ]; then
      printf '\n'
      ask 'Carpeta de instalación [~/.local/bin]: '
      if [ -n "$REPLY" ]; then
        INSTALL_DIR=$(expand_dir "$REPLY")
      fi
    fi
  fi
  check_dir_chars "$INSTALL_DIR"
  BIN=$INSTALL_DIR/$BIN_NAME
  note "Carpeta de instalación: $(pretty_dir "$INSTALL_DIR")"
}

# Informa de lo que ya hay instalado; puede terminar el script si el usuario no
# quiere reinstalar la misma versión.
check_existing() {
  if [ -d "$BIN" ] && [ ! -L "$BIN" ]; then
    die "$BIN es una carpeta, no un programa." "Elige otra carpeta de instalación."
  fi
  if [ ! -e "$BIN" ] && [ ! -L "$BIN" ]; then
    return 0
  fi
  current=''
  if [ -x "$BIN" ]; then
    current=$("$BIN" --version </dev/null 2>/dev/null | head -n 1 | tr -d '\r') || current=''
  fi
  if ! printf '%s' "$current" | grep -Eq '^v?[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9}$'; then
    warn "Ya hay un programa llamado $BIN_NAME en esa carpeta; se sustituirá por la versión $VERSION."
    return 0
  fi
  current=${current#v}
  if [ "$current" = "$VERSION" ]; then
    ok "Ya tienes la versión $VERSION instalada."
    if confirm '¿Quieres reinstalarla de todos modos? [s/N] ' n; then
      return 0
    fi
    if [ "$INTERACTIVE" != 1 ]; then
      note "Sin terminal interactiva se reinstala igualmente."
      return 0
    fi
    printf '\n'
    note 'No se ha cambiado nada.'
    note "Para configurarla de nuevo: $BIN setup"
    exit 0
  fi
  warn "Tienes la versión $current instalada; se sustituirá por la $VERSION."
}

# ---------------------------------------------------------------- paso 2

download_release() {
  base="$BASE_URL/download/$TAG"
  for f in "$ARCHIVE" checksums.txt checksums.txt.sig; do
    note "Descargando $f"
    download "$base/$f" "$TMP/$f" ||
      die "No se pudo descargar $f." \
        "Dirección: $base/$f" \
        "Comprueba tu conexión y que la versión $TAG exista." \
        "Instalación manual: $MANUAL_URL"
  done
  ok 'Descarga completa'
}

# ---------------------------------------------------------------- paso 3

verify_signature() {
  if ! command -v ssh-keygen >/dev/null 2>&1; then
    die 'No se encuentra ssh-keygen, necesario para verificar la firma de la descarga.' \
      'Por seguridad no se instala nada sin verificarla.' \
      'Instala el cliente OpenSSH (8.1 o posterior) y repite el comando.' \
      '  Debian/Ubuntu: sudo apt install openssh-client' \
      '  Fedora: sudo dnf install openssh-clients' \
      "Instalación manual: $MANUAL_URL"
  fi
  printf '%s %s\n' "$SIGN_NAMESPACE" "$SIGN_KEY" >"$TMP/allowed_signers"
  if out=$(ssh-keygen -Y verify -f "$TMP/allowed_signers" -I "$SIGN_NAMESPACE" \
    -n "$SIGN_NAMESPACE" -s "$TMP/checksums.txt.sig" <"$TMP/checksums.txt" 2>&1); then
    ok 'Firma de la versión verificada'
    return 0
  fi
  case $out in
    *'unknown option'* | *'illegal option'* | *'invalid option'* | *'unsupported option'* | *'sage: ssh-keygen'*)
      die 'Tu ssh-keygen es demasiado antiguo para verificar la firma (hace falta OpenSSH 8.1 o posterior).' \
        'Por seguridad no se instala nada sin verificarla.' \
        'Actualiza OpenSSH y repite el comando.' \
        "Instalación manual: $MANUAL_URL"
      ;;
  esac
  die 'La firma de la versión no es válida. No se instala nada.' \
    'Puede que la descarga esté dañada o manipulada. Vuelve a intentarlo más tarde.' \
    "Si el problema continúa, avisa en https://github.com/$REPO/issues"
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d ' ' -f 1
  else
    shasum -a 256 "$1" | cut -d ' ' -f 1
  fi
}

verify_checksum() {
  if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
    die 'No se encuentra sha256sum ni shasum para comprobar la descarga.' \
      "Instalación manual: $MANUAL_URL"
  fi
  want=$(awk -v n="$ARCHIVE" '$2 == n || $2 == "*" n { print $1 }' "$TMP/checksums.txt")
  case $want in
    '') die "checksums.txt no incluye $ARCHIVE. No se instala nada." ;;
    *"
"*) die "checksums.txt lista $ARCHIVE más de una vez. No se instala nada." ;;
  esac
  printf '%s' "$want" | grep -Eq '^[0-9A-Fa-f]{64}$' ||
    die "El checksum de $ARCHIVE tiene un formato no válido. No se instala nada."
  want=$(printf '%s' "$want" | tr 'A-F' 'a-f')
  got=$(sha256_of "$TMP/$ARCHIVE" | tr 'A-F' 'a-f')
  if [ "$got" != "$want" ]; then
    die 'El checksum SHA-256 del archivo descargado no coincide. No se instala nada.' \
      'Vuelve a intentarlo; si se repite, avisa en el repositorio.'
  fi
  ok 'Checksum SHA-256 correcto'
}

extract_binary() {
  mkdir "$TMP/extract"
  if ! tar -tzf "$TMP/$ARCHIVE" 2>/dev/null | grep -Fxq "$BIN_NAME"; then
    die "El archivo descargado no contiene $BIN_NAME. No se instala nada."
  fi
  # Solo se extrae esa entrada, por su nombre exacto.
  tar -xzf "$TMP/$ARCHIVE" -C "$TMP/extract" "$BIN_NAME" ||
    die "No se pudo extraer $BIN_NAME del archivo descargado."
  if [ ! -f "$TMP/extract/$BIN_NAME" ] || [ -L "$TMP/extract/$BIN_NAME" ]; then
    die "El archivo descargado no contiene un programa válido. No se instala nada."
  fi
  ok "Extraído $BIN_NAME"
}

# ---------------------------------------------------------------- paso 4

install_binary() {
  mkdir -p "$INSTALL_DIR" 2>/dev/null ||
    die "No se pudo crear la carpeta $INSTALL_DIR." \
      'Elige otra: AULAGLOBAL_INSTALL_DIR=/ruta/de/tu/carpeta'
  # Se copia a un temporal en la misma carpeta y se renombra: la sustitución es
  # atómica y un programa en ejecución no se corrompe.
  STAGED="$INSTALL_DIR/.$BIN_NAME-install.$$"
  rm -f "$STAGED" 2>/dev/null || true
  cp "$TMP/extract/$BIN_NAME" "$STAGED" 2>/dev/null ||
    die "No se puede escribir en $INSTALL_DIR." \
      'Elige otra carpeta o repite con los permisos necesarios.'
  chmod 0755 "$STAGED"
  mv -f "$STAGED" "$BIN" ||
    die "No se pudo instalar $BIN."
  STAGED=''
  ok "Instalado $BIN_NAME $VERSION en $(pretty_dir "$INSTALL_DIR")"
}

# ---------------------------------------------------------------- paso 5

path_has() {
  case ":${PATH:-}:" in
    *":$1:"*) return 0 ;;
  esac
  return 1
}

# add_line ARCHIVO LÍNEA: la añade al final si no está ya (idempotente).
add_line() {
  if [ -f "$1" ] && grep -Fxq "$2" "$1" 2>/dev/null; then
    note "$1 ya incluye la carpeta; no se toca."
    return 0
  fi
  if mkdir -p "$(dirname "$1")" 2>/dev/null && printf '\n%s\n' "$2" >>"$1" 2>/dev/null; then
    ok "Añadida al PATH en $1"
    PATH_CHANGED=1
  else
    warn "No se pudo escribir en $1."
    PATH_FAILED=1
  fi
}

configure_path() {
  if path_has "$INSTALL_DIR"; then
    ok 'La carpeta ya está en tu PATH'
    return 0
  fi
  if ! confirm "¿Añadir $(pretty_dir "$INSTALL_DIR") al PATH de tu terminal? [S/n] " y; then
    note 'No se ha modificado el PATH.'
    note "Para ejecutarlo, usa la ruta completa: $BIN"
    return 0
  fi
  shell_name=${SHELL:-}
  shell_name=${shell_name##*/}
  fish=0
  case $shell_name in
    zsh) profiles="${ZDOTDIR:-$HOME}/.zshrc" ;;
    bash)
      profiles="$HOME/.bashrc"
      # Terminal de macOS abre shells de login, que leen .bash_profile y no .bashrc.
      if [ "$(uname -s 2>/dev/null)" = Darwin ]; then
        profiles="$profiles
$HOME/.bash_profile"
      fi
      ;;
    fish)
      fish=1
      profiles="${XDG_CONFIG_HOME:-$HOME/.config}/fish/config.fish"
      ;;
    *) profiles="$HOME/.profile" ;;
  esac
  dir_expr=$INSTALL_DIR
  case $INSTALL_DIR in
    "$HOME") dir_expr="\$HOME" ;;
    "$HOME"/*) dir_expr="\$HOME/${INSTALL_DIR#"$HOME"/}" ;;
  esac
  if [ "$fish" = 1 ]; then
    line="set -gx PATH \"$dir_expr\" \$PATH # aulaglobal-mcp"
  else
    line="export PATH=\"$dir_expr:\$PATH\" # aulaglobal-mcp"
  fi
  PATH_CHANGED=0
  PATH_FAILED=0
  old_ifs=$IFS
  IFS='
'
  for profile in $profiles; do
    IFS=$old_ifs
    add_line "$profile" "$line"
    IFS='
'
  done
  IFS=$old_ifs
  if [ "$PATH_CHANGED" = 1 ]; then
    warn 'Abre una terminal nueva para que el cambio surta efecto.'
  fi
  if [ "$PATH_FAILED" = 1 ]; then
    note "Añade esta carpeta a tu PATH a mano: $INSTALL_DIR"
  fi
}

# ---------------------------------------------------------------- final

run_setup() {
  other=$(command -v "$BIN_NAME" 2>/dev/null || true)
  if [ -n "$other" ] && [ "$other" != "$BIN" ]; then
    warn "Hay otra copia en $other que puede ejecutarse antes que esta."
  fi
  printf '\n'
  if [ "$HAVE_TTY" != 1 ]; then
    note 'No hay terminal interactiva, así que no se ejecuta setup.'
    note "Cuando quieras configurarlo, ejecuta: $BIN setup"
    return 0
  fi
  note 'Ahora se abre el asistente de configuración (setup).'
  printf '\n'
  if "$BIN" setup </dev/tty; then
    :
  else
    printf '\n'
    warn 'setup no terminó correctamente.'
    note "Puedes repetirlo cuando quieras: $BIN setup"
  fi
}

main() {
  printf '%sInstalación de aulaglobal-mcp%s\n\n' "$BOLD" "$RESET"
  if [ "${AULAGLOBAL_YES:-}" = 1 ]; then
    note 'AULAGLOBAL_YES=1: se aceptan las opciones predeterminadas.'
  elif [ "$HAVE_TTY" != 1 ]; then
    note 'No hay terminal interactiva: se usan las opciones predeterminadas.'
  fi

  step 1 'Preparando la instalación'
  for t in uname mktemp tar grep sed awk tr head cut cp mv rm mkdir chmod; do
    need "$t"
  done
  if command -v curl >/dev/null 2>&1; then
    FETCH=curl
  elif command -v wget >/dev/null 2>&1; then
    FETCH=wget
  else
    die 'Hace falta curl o wget para descargar el programa.' "Instalación manual: $MANUAL_URL"
  fi
  # La dirección base solo se cambia en las pruebas del propio instalador.
  BASE_URL=${AULAGLOBAL_RELEASE_BASE_URL:-https://github.com/$REPO/releases}
  CURL_SEC=''
  if [ -z "${AULAGLOBAL_RELEASE_BASE_URL:-}" ]; then
    CURL_SEC="--proto =https --proto-redir =https --tlsv1.2"
  fi
  detect_platform
  note "Sistema: $PLATFORM_NAME"
  choose_version
  choose_dir
  check_existing
  TMP=$(mktemp -d "${TMPDIR:-/tmp}/aulaglobal-install.XXXXXX") ||
    die 'No se pudo crear una carpeta temporal.'

  step 2 'Descargando'
  download_release

  step 3 'Verificando la descarga'
  verify_signature
  verify_checksum
  extract_binary

  step 4 'Instalando'
  install_binary

  step 5 'Preparando la terminal'
  configure_path

  run_setup
}

main "$@"
