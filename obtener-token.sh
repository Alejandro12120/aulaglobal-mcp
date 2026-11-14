#!/usr/bin/env bash
set -euo pipefail
umask 077
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
blue='' reset=''
if [[ -t 1 ]]; then blue=$'\033[1;34m'; reset=$'\033[0m'; fi
say() { printf '%s%s%s\n' "$blue" "$*" "$reset"; }
if ! { exec 3<>/dev/tty; } 2>/dev/null; then
    say 'Abre una terminal y ejecuta ./obtener-token.sh.'
    exit 1
fi
ask() { printf '%s ' "$1"; IFS= read -r reply <&3 || reply=''; }
bin=${AULAGLOBAL_MCP_BIN:-./aulaglobal-mcp}
if [[ ! -x "$bin" ]]; then
    say "No encuentro el programa ejecutable: $bin"
    if ! command -v docker >/dev/null 2>&1; then
        say 'Instala Docker y sigue el Paso 1 del README.md para compilar.'
        exit 1
    fi
    ask '¿Quieres compilarlo con Docker? [s/N]'
    case "$reply" in
        s|S|si|sí)
            docker run --rm -u "$(id -u):$(id -g)" -e GOCACHE=/tmp/gocache -e GOPATH=/tmp/gopath \
                -v "$PWD":/src -w /src golang:1.26.9 sh -c 'CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o aulaglobal-mcp ./cmd/aulaglobal-mcp'
            bin=./aulaglobal-mcp ;;
        *) say 'Puedes compilarlo después siguiendo el Paso 1 del README.md.'; exit 0 ;;
    esac
fi
mkdir -p secrets data
chmod 700 secrets data
set --
if [[ -e secrets/token ]]; then
    say 'Ya tienes un token guardado.'
    if [[ -f secrets/token.meta.json ]]; then
        obtained=$(sed -n 's/.*"obtained_at"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' secrets/token.meta.json)
        [[ -z "$obtained" ]] || printf 'Obtenido el: %s\n' "$obtained"
    fi
    ask '¿Quieres reemplazarlo? [s/N]'
    case "$reply" in
        s|S|si|sí) set -- --force ;;
        *) say 'Conservamos tu token.'; exit 0 ;;
    esac
fi
cat <<'STEPS'
1. Entra en https://aulaglobal.uc3m.es e inicia sesión.
2. Pulsa F12 (Mac: Cmd + Option + I) y abre Consola.
3. Pega este comando y pulsa Enter para copiar la cookie:
copy(("; " + document.cookie).split("; MoodleSessionag=").pop().split(";").shift())
Si Chrome bloquea pegar, escribe «allow pasting» (o «permitir pegar») primero.
Alternativa: Aplicación → Cookies → https://aulaglobal.uc3m.es →
MoodleSessionag. Copia su valor (Firefox: Almacenamiento → Cookies).
4. Vuelve aquí y pega la cookie. No se verá mientras escribes.
No compartas la cookie ni el token.
STEPS
export -n cookie 2>/dev/null || true
for attempt in 1 2 3; do
    printf 'Pega la cookie y pulsa Enter (intento %s/3): ' "$attempt"
    cookie=''
    IFS= read -rs cookie <&3 || true
    printf '\n'
    # shellcheck disable=SC2034 # Three normalization rounds handle nested wrappers.
    for pass in 1 2 3; do
        cookie="${cookie#"${cookie%%[![:space:]]*}"}"
        cookie="${cookie%"${cookie##*[![:space:]]}"}"
        case "$cookie" in
            \"*\") cookie=${cookie#\"}; cookie=${cookie%\"} ;;
            \'*\') cookie=${cookie#\'}; cookie=${cookie%\'} ;;
        esac
        cookie=${cookie#MoodleSessionag=}
    done
    [[ -z "$cookie" ]] || break
    say 'No has pegado ningún valor. Copia el valor de MoodleSessionag e inténtalo otra vez.'
done
if [[ -z "$cookie" ]]; then unset cookie; exit 1; fi
# Only stderr is captured. The binary redacts credentials in its errors.
if error=$(printf '%s' "$cookie" | "$bin" token-from-cookie --output secrets/token "$@" 2>&1 >/dev/null); then
    unset cookie
else
    unset cookie
    case "$error" in
        *'must not already exist'*) say 'Ya existe un archivo del token. Ejecuta otra vez y acepta reemplazarlo.' ;;
        *) say 'No se pudo obtener el token. Cierra sesión, vuelve a entrar en AulaGlobal y ejecuta ./obtener-token.sh otra vez.' ;;
    esac
    printf 'Detalle del programa:\n%s\n' "$error" >&2
    exit 1
fi
say 'Token guardado en secrets/token con permisos privados.'
if [[ ${AULAGLOBAL_SKIP_VERIFY:-0} != 1 ]]; then
    if command -v curl >/dev/null 2>&1; then
        if response=$(curl -fsS --connect-timeout 10 --max-time 30 -X POST \
            https://aulaglobal.uc3m.es/webservice/rest/server.php \
            --data-urlencode 'wstoken@secrets/token' \
            -d wsfunction=core_webservice_get_site_info -d moodlewsrestformat=json 2>/dev/null); then
            fullname=$(printf '%s' "$response" | sed -n 's/.*"fullname"[[:space:]]*:[[:space:]]*"\([^"\\]*\)".*/\1/p')
            if [[ -n "$fullname" && "$response" != *'"exception"'* ]]; then
                printf 'Token válido. Hola, %s\n' "$fullname"
            else
                say 'Aviso: no se pudo confirmar el token. Comprueba auth_status o vuelve a obtenerlo.'
            fi
        else
            say 'Aviso: no se pudo conectar para verificar. Comprueba después con auth_status.'
        fi
    else
        say 'No tienes curl; comprueba el token con auth_status en tu asistente.'
    fi
fi
say 'Siguiente paso: README.md → Paso 3 · Conectarlo con tu asistente (Claude Code / Codex).'
