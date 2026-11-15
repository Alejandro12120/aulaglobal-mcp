#!/bin/bash
# shellcheck disable=SC2002,SC2016,SC2088  # cat|sh imita "curl | sh"; las comillas simples son literales a propósito
# Pruebas de extremo a extremo de install.sh. Se ejecuta DENTRO del contenedor
# golang que lanza scripts/test-install.sh (usuario root, repositorio en /src de
# solo lectura, trabajo en /work). No usar fuera de un contenedor desechable:
# instala paquetes con apt.
set -u

SRC=${SRC:-/src}
W=${W:-/work}
PORT=8099
BASE="http://127.0.0.1:$PORT/releases"
DEFPATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
export GOCACHE=${GOCACHE:-$W/gocache} GOMODCACHE=${GOMODCACHE:-$W/gomod}

pass=0
fail=0
CUR=''
OUT=''
RC=0
C=''

say() { printf '\n== %s\n' "$*"; }

check() { # descripción comando...
  local d=$1
  shift
  if "$@"; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    printf 'FALLO [%s] %s\n' "$CUR" "$d"
    printf '%s\n' "$OUT" | sed 's/^/    | /' | tail -n 25
  fi
}
has() { [[ $OUT == *"$1"* ]]; }
hasnt() { [[ $OUT != *"$1"* ]]; }
rc_is() { [[ $RC -eq $1 ]]; }
rc_not0() { [[ $RC -ne 0 ]]; }
eq() { [[ $1 == "$2" ]]; }
count_lines() { grep -c -F -- "$1" "$2" 2>/dev/null; } # líneas que contienen la cadena fija
no_file() { [[ ! -e $1 ]]; }
dir_empty() { [[ -z $(ls -A "$1" 2>/dev/null) ]]; }

# ------------------------------------------------------------------ preparación

say "Precondiciones"
if ( : </dev/tty ) 2>/dev/null; then
  echo "este arnés debe ejecutarse SIN terminal (docker run sin -t)"
  exit 2
fi
mkdir -p "${W:?}/bin" "${W:?}/keys" "${W:?}/cases" "${W:?}/tb"

if ! command -v busybox >/dev/null 2>&1; then
  say "Instalando busybox-static (solo dentro de este contenedor)"
  if ! { DEBIAN_FRONTEND=noninteractive apt-get update -qq >/dev/null 2>&1 && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends busybox-static >/dev/null 2>&1; }; then
    echo "no se pudo instalar busybox-static"
    exit 2
  fi
fi
for t in dash bash busybox script ssh-keygen curl wget go; do
  PATH=$DEFPATH command -v "$t" >/dev/null || {
    echo "falta $t en la imagen"
    exit 2
  }
done
ssh -V 2>&1 | head -1
case $(uname -m) in
  x86_64) HOSTLABEL=linux_x64 ;;
  aarch64) HOSTLABEL=linux_arm64 ;;
  *)
    echo "arquitectura de prueba no soportada: $(uname -m)"
    exit 2
    ;;
esac

say "Compilando binarios reales y herramientas de prueba"
cd "$SRC" || exit 2
for v in 0.0.1 0.0.2; do
  CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags "-s -w -X main.version=$v" -o "$W/bin/aulaglobal-mcp-$v" ./cmd/aulaglobal-mcp || exit 2
done
go build -buildvcs=false -o "$W/bin/testserver" scripts/test-install/server.go || exit 2
go build -buildvcs=false -o "$W/bin/mkrelease" scripts/test-install/mkrelease.go || exit 2

say "Clave de prueba, versiones falsas firmadas y servidor local"
rm -f "${W:?}/keys/test" "${W:?}/keys/test.pub"
ssh-keygen -q -t ed25519 -N '' -C aulaglobal-mcp-update -f "$W/keys/test" || exit 2
TESTKEY="$(cut -d' ' -f1,2 "$W/keys/test.pub") aulaglobal-mcp-update"
rm -rf "${W:?}/srv"
mkdir -p "$W/srv"
mk() { "$W/bin/mkrelease" -out "$W/srv" -key "$W/keys/test" -extra "$SRC/install.sh" -extra "$SRC/install.ps1" "$@" || exit 2; }
mk -version 0.0.1 -payload "$HOSTLABEL=$W/bin/aulaglobal-mcp-0.0.1"
mk -version 0.0.2 -payload "$HOSTLABEL=$W/bin/aulaglobal-mcp-0.0.2"
mk -version 0.0.3 # programa falso: imprime 0.0.3 y marca si setup recibe un terminal
echo v0.0.2 >"$W/latest-tag"
# PREPARE_ONLY=1: solo se dejan listos los artefactos (los usa inner.ps1).
[[ ${PREPARE_ONLY:-} == 1 ]] && exit 0
"$W/bin/testserver" -dir "$W/srv" -latest "$W/latest-tag" -addr "127.0.0.1:$PORT" 2>"$W/server.log" &
SERVER_PID=$!
trap 'kill $SERVER_PID 2>/dev/null' EXIT
for _ in 1 2 3 4 5 6 7 8 9 10; do
  curl -fsS -o /dev/null "$BASE/tag/x" 2>/dev/null && break
  sleep 0.5
done
curl -fsS -o /dev/null "$BASE/tag/x" || {
  echo "el servidor local no arranca"
  exit 2
}

# Copia de prueba del instalador: SOLO cambia la línea de la clave pública.
TESTSH=$W/install.test.sh
ORIGSH=$SRC/install.sh
sed "s|^SIGN_KEY='.*'\$|SIGN_KEY='$TESTKEY'|" "$ORIGSH" >"$TESTSH"
CUR=prep
OUT=$(diff "$ORIGSH" "$TESTSH")
check "la copia de prueba difiere del original solo en una línea" eq "$(diff "$ORIGSH" "$TESTSH" | grep -c '^<')" 1
check "el instalador no tiene opciones para saltarse la firma ni cambiar la clave" eq "$(grep -ci 'skip.*sig\|insecure\|AULAGLOBAL_[A-Z_]*KEY' "$ORIGSH")" 0
check "no se usa xattr" eq "$(grep -c 'xattr' "$ORIGSH")" 0

# Carpetas de herramientas: PATH restringido sin alguna herramienta.
mktb() { # nombre excluidas...
  local d=$W/tb/$1 f b x skip
  shift
  rm -rf "${d:?}"
  mkdir -p "$d"
  for f in /usr/bin/* /usr/local/bin/*; do
    b=${f##*/}
    skip=0
    for x in "$@"; do [[ $b == "$x" ]] && skip=1; done
    [[ $skip == 1 || -e $d/$b ]] || ln -s "$f" "$d/$b"
  done
  echo "$d"
}
TB_NOCURL=$(mktb nocurl curl)
TB_NOFETCH=$(mktb nofetch curl wget)
TB_NOSSH=$(mktb nossh ssh-keygen)
TB_FAKE=$(mktb fake uname sysctl)
cat >"$TB_FAKE/uname" <<'EOF'
#!/bin/sh
case "$1" in
  -s) echo "${FAKE_S:-Linux}" ;;
  -m) echo "${FAKE_M:-x86_64}" ;;
  *) exec /usr/bin/uname "$@" ;;
esac
EOF
cat >"$TB_FAKE/sysctl" <<'EOF'
#!/bin/sh
if [ "${FAKE_ROSETTA:-0}" = 1 ]; then echo 1; else echo 0; fi
EOF
chmod +x "$TB_FAKE/uname" "$TB_FAKE/sysctl"
TB_OLDSSH=$(mktb oldssh ssh-keygen)
cat >"$TB_OLDSSH/ssh-keygen" <<'EOF'
#!/bin/sh
echo "ssh-keygen: unknown option -- Y" >&2
echo "usage: ssh-keygen [-q] [-b bits] ..." >&2
exit 1
EOF
chmod +x "$TB_OLDSSH/ssh-keygen"

run_in() { # carpeta shell script [VAR=valor...]; TPATH=ruta fija el PATH del instalador
  C=$1
  local sh=$2 script=$3 a tp=$DEFPATH args=()
  shift 3
  for a in "$@"; do
    case $a in
      TPATH=*) tp=${a#TPATH=} ;;
      *) args+=("$a") ;;
    esac
  done
  # $sh sin comillas a propósito: puede ser «busybox ash».
  # shellcheck disable=SC2086
  OUT=$(cat "$script" | env -i PATH="$tp" HOME="$C/home" TMPDIR="$C/tmp" SHELL=/bin/sh \
    "AULAGLOBAL_RELEASE_BASE_URL=$BASE" "${args[@]}" $sh 2>&1)
  RC=$?
}
fresh() { # nombre -> crea la carpeta de caso vacía y deja su ruta en C
  C=$W/cases/$1
  rm -rf "${C:?}"
  mkdir -p "$C/home" "$C/tmp"
}
inst() { # nombre shell script [VAR=valor...]
  local name=$1
  shift
  fresh "$name"
  run_in "$C" "$@"
}
ver_of() { "$1" --version 2>&1; }
tmp_clean() { dir_empty "$C/tmp"; }

# restaura lo servido tras manipularlo
rm -rf "${W:?}/srv.orig"
cp -a "$W/srv" "$W/srv.orig"
restore_srv() {
  rm -rf "${W:?}/srv"
  cp -a "$W/srv.orig" "$W/srv"
}
REL2=$W/srv/releases/download/v0.0.2
ARCH2=$REL2/aulaglobal-mcp_0.0.2_$HOSTLABEL.tar.gz
SUMS2=$REL2/checksums.txt
SIG2=$REL2/checksums.txt.sig

# ------------------------------------------------------------------ casos sin terminal

suite() { # etiqueta shell
  local t=$1 sh=$2 bin badv
  say "Shell: $t"

  CUR="$t/instalación básica (curl | sh, sin terminal, YES)"
  restore_srv
  inst "$t-basic" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-basic/home/bin"
  bin=$C/home/bin/aulaglobal-mcp
  check "código 0" rc_is 0
  check "binario instalado y ejecutable" test -x "$bin"
  check "versión 0.0.2 (la última)" eq "$(ver_of "$bin")" 0.0.2
  check "permisos 755" eq "$(stat -c %a "$bin")" 755
  check "paso 1/5" has '[1/5] Preparando la instalación'
  check "paso 5/5" has '[5/5] Preparando la terminal'
  check "firma verificada" has 'Firma de la versión verificada'
  check "checksum correcto" has 'Checksum SHA-256 correcto'
  check "no se ejecuta setup sin terminal" has 'no se ejecuta setup'
  check "setup no dejó rastro" no_file "$C/home/.config/aulaglobal-mcp"
  check "solo el binario en la carpeta" eq "$(ls -A "$C/home/bin")" aulaglobal-mcp
  check "sin temporales" tmp_clean
  check "perfil por defecto .profile con una línea" eq "$(count_lines '# aulaglobal-mcp' "$C/home/.profile")" 1
  check "contenido de la línea" eq "$(grep -F '# aulaglobal-mcp' "$C/home/.profile")" 'export PATH="$HOME/bin:$PATH" # aulaglobal-mcp'

  CUR="$t/segunda ejecución: misma versión e idempotencia del PATH"
  run_in "$C" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$C/home/bin"
  check "código 0" rc_is 0
  check "avisa de que ya está instalada" has 'Ya tienes la versión 0.0.2 instalada'
  check "sigue una sola línea en .profile" eq "$(count_lines '# aulaglobal-mcp' "$C/home/.profile")" 1
  check "avisa de que el perfil ya la incluye" has 'ya incluye la carpeta'
  check "versión intacta" eq "$(ver_of "$bin")" 0.0.2

  CUR="$t/actualización 0.0.1 -> 0.0.2"
  inst "$t-upgrade" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_VERSION=v0.0.1 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-upgrade/home/bin"
  check "instala la versión fijada 0.0.1" eq "$(ver_of "$C/home/bin/aulaglobal-mcp")" 0.0.1
  check "menciona la versión elegida" has 'Versión elegida: v0.0.1'
  run_in "$C" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$C/home/bin"
  check "informa del reemplazo" has 'Tienes la versión 0.0.1 instalada; se sustituirá por la 0.0.2'
  check "queda la 0.0.2" eq "$(ver_of "$C/home/bin/aulaglobal-mcp")" 0.0.2

  CUR="$t/versión fijada sin v"
  inst "$t-pin2" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_VERSION=0.0.1 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-pin2/bin"
  check "acepta 0.0.1" eq "$(ver_of "$C/bin/aulaglobal-mcp")" 0.0.1

  for badv in 1.2 v1.2.3-rc1 latest 'v1.2.3;id' v1.2.3.4; do
    CUR="$t/versión no válida «$badv»"
    inst "$t-badver" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_VERSION="$badv" AULAGLOBAL_INSTALL_DIR="$W/cases/$t-badver/bin"
    check "falla" rc_not0
    check "mensaje" has 'no es válida'
    check "nada instalado" no_file "$C/bin"
  done

  CUR="$t/versión inexistente"
  inst "$t-nover" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_VERSION=v0.0.99 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-nover/bin"
  check "falla" rc_not0
  check "mensaje de descarga" has 'No se pudo descargar'
  check "nada instalado" no_file "$C/bin"
  check "sin temporales" tmp_clean

  CUR="$t/sin YES ni terminal: valores por defecto"
  inst "$t-defaults" "$sh" "$TESTSH" SHELL=/bin/zsh
  check "código 0" rc_is 0
  check "avisa de que no hay terminal" has 'No hay terminal interactiva: se usan las opciones predeterminadas'
  check "instala en ~/.local/bin" test -x "$C/home/.local/bin/aulaglobal-mcp"
  check "zsh -> .zshrc" eq "$(grep -F '# aulaglobal-mcp' "$C/home/.zshrc")" 'export PATH="$HOME/.local/bin:$PATH" # aulaglobal-mcp'

  CUR="$t/fish"
  inst "$t-fish" "$sh" "$TESTSH" AULAGLOBAL_YES=1 SHELL=/usr/bin/fish
  check "sintaxis de fish" eq "$(grep -F '# aulaglobal-mcp' "$C/home/.config/fish/config.fish")" 'set -gx PATH "$HOME/.local/bin" $PATH # aulaglobal-mcp'
  run_in "$C" "$sh" "$TESTSH" AULAGLOBAL_YES=1 SHELL=/usr/bin/fish
  check "fish idempotente" eq "$(count_lines '# aulaglobal-mcp' "$C/home/.config/fish/config.fish")" 1

  CUR="$t/bash en Linux"
  inst "$t-bash" "$sh" "$TESTSH" AULAGLOBAL_YES=1 SHELL=/bin/bash
  check ".bashrc" eq "$(count_lines '# aulaglobal-mcp' "$C/home/.bashrc")" 1
  check "no se toca .bash_profile en Linux" no_file "$C/home/.bash_profile"

  CUR="$t/carpeta ya en el PATH"
  inst "$t-inpath" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-inpath/home/bin" \
    "PATH=$W/cases/$t-inpath/home/bin:$DEFPATH"
  check "no toca ningún perfil" no_file "$C/home/.profile"
  check "lo indica" has 'La carpeta ya está en tu PATH'

  CUR="$t/~ y espacios en AULAGLOBAL_INSTALL_DIR"
  inst "$t-tilde" "$sh" "$TESTSH" AULAGLOBAL_YES=1 'AULAGLOBAL_INSTALL_DIR=~/mi carpeta/bin'
  check "expande ~ y admite espacios" test -x "$C/home/mi carpeta/bin/aulaglobal-mcp"
  check "línea con espacios" eq "$(grep -F '# aulaglobal-mcp' "$C/home/.profile")" 'export PATH="$HOME/mi carpeta/bin:$PATH" # aulaglobal-mcp'
  inst "$t-badchar" "$sh" "$TESTSH" AULAGLOBAL_YES=1 'AULAGLOBAL_INSTALL_DIR=/tmp/a"b'
  check "rechaza comillas en la ruta" rc_not0

  CUR="$t/archivo manipulado (checksum)"
  restore_srv
  printf 'x' >>"$ARCH2"
  inst "$t-badarch" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-badarch/bin"
  check "falla" rc_not0
  check "mensaje de checksum" has 'checksum SHA-256 del archivo descargado no coincide'
  check "la firma sí era correcta" has 'Firma de la versión verificada'
  check "nada instalado" no_file "$C/bin"
  check "sin perfil" no_file "$C/home/.profile"
  check "sin temporales" tmp_clean

  CUR="$t/checksums.txt manipulado (firma)"
  restore_srv
  # Cambia el primer carácter del archivo sin alterar su longitud.
  if [[ $(head -c 1 "$SUMS2") == 0 ]]; then repl=1; else repl=0; fi
  { printf '%s' "$repl"; tail -c +2 "$SUMS2"; } >"$SUMS2.new" && mv "$SUMS2.new" "$SUMS2"
  inst "$t-badsums" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-badsums/bin"
  check "falla" rc_not0
  check "mensaje de firma" has 'La firma de la versión no es válida'
  check "no llega al checksum" hasnt 'Checksum SHA-256 correcto'
  check "nada instalado" no_file "$C/bin"
  check "sin temporales" tmp_clean

  CUR="$t/firma ausente"
  restore_srv
  rm -f "$SIG2"
  inst "$t-nosig" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-nosig/bin"
  check "falla" rc_not0
  check "nada instalado" no_file "$C/bin"
  restore_srv

  CUR="$t/firma de otra clave (instalador publicado vs versión de prueba)"
  inst "$t-wrongkey" "$sh" "$ORIGSH" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-wrongkey/bin"
  check "falla" rc_not0
  check "mensaje de firma" has 'La firma de la versión no es válida'
  check "nada instalado" no_file "$C/bin"

  CUR="$t/sin ssh-keygen"
  inst "$t-nossh" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-nossh/bin" TPATH="$TB_NOSSH"
  check "falla" rc_not0
  check "mensaje" has 'No se encuentra ssh-keygen'
  check "enlace manual" has 'https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest'
  check "nada instalado" no_file "$C/bin"

  CUR="$t/ssh-keygen antiguo"
  inst "$t-oldssh" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-oldssh/bin" TPATH="$TB_OLDSSH"
  check "falla" rc_not0
  check "mensaje" has 'demasiado antiguo'
  check "nada instalado" no_file "$C/bin"

  # busybox-static ejecuta los applets (uname, wget...) sin mirar el PATH: no se pueden sustituir.
  if [[ $t != busybox-ash ]]; then
  CUR="$t/sistema no soportado"
  inst "$t-arch" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-arch/bin" TPATH="$TB_FAKE:$DEFPATH" FAKE_M=riscv64
  check "falla" rc_not0
  check "mensaje" has 'No hay versión publicada para este sistema (Linux riscv64)'
  check "enlace manual" has 'releases/latest'
  inst "$t-os" "$sh" "$TESTSH" AULAGLOBAL_YES=1 TPATH="$TB_FAKE:$DEFPATH" FAKE_S=FreeBSD
  check "SO no soportado" has 'No hay versión publicada para este sistema (FreeBSD'
  inst "$t-win" "$sh" "$TESTSH" AULAGLOBAL_YES=1 TPATH="$TB_FAKE:$DEFPATH" FAKE_S=MINGW64_NT-10.0
  check "Git Bash apunta a install.ps1" has 'install.ps1'
  fi

  CUR="$t/solo wget (sin curl)"
  inst "$t-wget" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-wget/bin" TPATH="$TB_NOCURL"
  check "código 0" rc_is 0
  check "versión 0.0.2 resuelta con wget" eq "$(ver_of "$C/bin/aulaglobal-mcp")" 0.0.2
  if [[ $t != busybox-ash ]]; then
  inst "$t-nofetch" "$sh" "$TESTSH" AULAGLOBAL_YES=1 TPATH="$TB_NOFETCH"
  check "sin curl ni wget falla" rc_not0
  check "mensaje" has 'Hace falta curl o wget'
  fi

  if [[ $t != busybox-ash ]]; then
  CUR="$t/macOS y ARM simulados"
  inst "$t-mac" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_VERSION=v0.0.3 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-mac/home/bin" \
    TPATH="$TB_FAKE:$DEFPATH" FAKE_S=Darwin FAKE_M=arm64 SHELL=/bin/bash
  check "Apple Silicon" has 'Sistema: macOS (Apple Silicon)'
  check "instala el archivo macos_apple-silicon" eq "$(ver_of "$C/home/bin/aulaglobal-mcp")" 0.0.3
  check ".bashrc" eq "$(count_lines '# aulaglobal-mcp' "$C/home/.bashrc")" 1
  check ".bash_profile en macOS" eq "$(count_lines '# aulaglobal-mcp' "$C/home/.bash_profile")" 1
  inst "$t-macintel" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_VERSION=v0.0.3 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-macintel/home/bin" \
    TPATH="$TB_FAKE:$DEFPATH" FAKE_S=Darwin FAKE_M=x86_64
  check "Intel" has 'Sistema: macOS (Intel)'
  check "instalado (macos_intel)" test -x "$C/home/bin/aulaglobal-mcp"
  inst "$t-macrosetta" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_VERSION=v0.0.3 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-macrosetta/home/bin" \
    TPATH="$TB_FAKE:$DEFPATH" FAKE_S=Darwin FAKE_M=x86_64 FAKE_ROSETTA=1
  check "Rosetta -> Apple Silicon" has 'Sistema: macOS (Apple Silicon)'
  inst "$t-linarm" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_VERSION=v0.0.3 AULAGLOBAL_INSTALL_DIR="$W/cases/$t-linarm/home/bin" \
    TPATH="$TB_FAKE:$DEFPATH" FAKE_M=aarch64
  check "Linux ARM64" has 'Sistema: Linux (ARM64)'
  check "instalado (linux_arm64)" test -x "$C/home/bin/aulaglobal-mcp"
  fi

  CUR="$t/carpeta imposible"
  fresh "$t-nowrite"
  : >"$C/afile"
  run_in "$C" "$sh" "$TESTSH" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$C/afile/bin"
  check "falla" rc_not0
  check "mensaje" has 'No se pudo crear la carpeta'
  check "sin temporales" tmp_clean

  CUR="$t/script truncado no hace nada"
  fresh "$t-trunc"
  head -c 9000 "$TESTSH" >"$C/trunc.sh"
  run_in "$C" "$sh" "$C/trunc.sh" AULAGLOBAL_YES=1 AULAGLOBAL_INSTALL_DIR="$C/bin2"
  check "falla" rc_not0
  check "no instala nada" no_file "$C/bin2"
  check "no descarga nada" hasnt 'Descargando'
}

# ------------------------------------------------------------------ casos con terminal (pty vía script)

tty_run() { # carpeta shell respuestas script [VAR=valor...]
  local c=$1 sh=$2 answers=$3 script=$4 v
  shift 4
  C=$c
  {
    echo '#!/bin/sh'
    printf 'cat %q | env -i PATH=%q HOME=%q TMPDIR=%q NO_COLOR=1 AULAGLOBAL_RELEASE_BASE_URL=%q' \
      "$script" "$DEFPATH" "$C/home" "$C/tmp" "$BASE"
    for v in "$@"; do printf ' %q' "$v"; done
    printf ' %s\n' "$sh"
  } >"$C/run.sh"
  printf '%b' "$answers" | script -qec "sh $C/run.sh" /dev/null >"$C/out.txt" 2>&1
  RC=$?
  OUT=$(tr -d '\r' <"$C/out.txt")
}

tty_suite() {
  local t=$1 sh=$2
  say "Shell con terminal: $t"

  CUR="$t/tty: carpeta personalizada, PATH sí, setup con terminal"
  fresh "$t-tty1"
  tty_run "$C" "$sh" '~/mybin\ns\n' "$TESTSH" AULAGLOBAL_VERSION=v0.0.3 SHELL=/bin/bash
  check "pregunta la carpeta" has 'Carpeta de instalación [~/.local/bin]:'
  check "instala en la carpeta elegida" test -x "$C/home/mybin/aulaglobal-mcp"
  check "pregunta por el PATH" has 'al PATH de tu terminal? [S/n]'
  check "línea en .bashrc" eq "$(grep -F '# aulaglobal-mcp' "$C/home/.bashrc")" 'export PATH="$HOME/mybin:$PATH" # aulaglobal-mcp'
  check "pide abrir una terminal nueva" has 'Abre una terminal nueva'
  check "ejecuta setup" has 'STUB-SETUP-RAN setup'
  check "setup recibe un terminal" has 'STUB-STDIN-IS-TTY'

  CUR="$t/tty: Enter = predeterminados; PATH n"
  fresh "$t-tty2"
  tty_run "$C" "$sh" '\nn\n' "$TESTSH" AULAGLOBAL_VERSION=v0.0.3
  check "carpeta predeterminada" test -x "$C/home/.local/bin/aulaglobal-mcp"
  check "PATH rechazado: sin perfil" no_file "$C/home/.profile"
  check "indica la ruta completa" has 'usa la ruta completa'
  check "setup igualmente" has 'STUB-SETUP-RAN setup'

  CUR="$t/tty: misma versión, no reinstalar"
  fresh "$t-tty3"
  mkdir -p "$C/home/bin"
  printf '#!/bin/sh\necho 0.0.3\n' >"$C/home/bin/aulaglobal-mcp"
  chmod +x "$C/home/bin/aulaglobal-mcp"
  cp "$C/home/bin/aulaglobal-mcp" "$C/orig"
  tty_run "$C" "$sh" 'n\n' "$TESTSH" AULAGLOBAL_VERSION=v0.0.3 AULAGLOBAL_INSTALL_DIR="$C/home/bin"
  check "código 0" rc_is 0
  check "pregunta si reinstalar" has '¿Quieres reinstalarla de todos modos? [s/N]'
  check "no cambia nada" has 'No se ha cambiado nada'
  check "no ejecuta setup" hasnt 'STUB-SETUP-RAN'
  check "binario intacto" cmp -s "$C/orig" "$C/home/bin/aulaglobal-mcp"

  CUR="$t/tty: misma versión, reinstalar"
  tty_run "$C" "$sh" 's\nn\n' "$TESTSH" AULAGLOBAL_VERSION=v0.0.3 AULAGLOBAL_INSTALL_DIR="$C/home/bin"
  check "reinstala" hasnt 'No se ha cambiado nada'
  check "binario sustituido" bash -c '! cmp -s "$1" "$2"' _ "$C/orig" "$C/home/bin/aulaglobal-mcp"
  check "ejecuta setup" has 'STUB-SETUP-RAN setup'
}

for spec in 'dash:dash' 'bash:bash' 'busybox-ash:busybox ash'; do
  suite "${spec%%:*}" "${spec#*:}"
done
for spec in 'dash:dash' 'bash:bash' 'busybox-ash:busybox ash'; do
  tty_suite "${spec%%:*}" "${spec#*:}"
done

say "Resultado: $pass comprobaciones correctas, $fail fallidas"
[[ $fail -eq 0 ]]
