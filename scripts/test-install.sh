#!/bin/sh
# Pruebas de extremo a extremo de los instaladores (install.sh e install.ps1)
# contra una versión falsa firmada con una clave desechable y servida por un
# servidor local: no se toca GitHub ni se instala nada en tu máquina.
#
#   scripts/test-install.sh          # install.sh (dash, bash, busybox ash) e install.ps1 (pwsh)
#   scripts/test-install.sh sh       # solo install.sh
#   scripts/test-install.sh ps       # solo install.ps1
#
# Hace falta Docker. Todo ocurre en contenedores desechables con el repositorio
# montado de solo lectura; el único estado es un volumen temporal que se borra al
# terminar. Variables opcionales:
#   GO_IMAGE   imagen con Go y OpenSSH (golang:1.26.9)
#   PS_IMAGE   imagen con PowerShell 7 (mcr.microsoft.com/powershell:ubuntu-24.04)
#   GOCACHE_VOLUME  volumen Docker con la caché de Go para acelerar compilaciones
# Las imágenes se descargan si faltan y se dejan: bórralas con `docker rmi` si ya
# no las quieres.
set -eu

what=${1:-all}
case $what in
  all | sh | ps) ;;
  *)
    echo "uso: $0 [all|sh|ps]" >&2
    exit 2
    ;;
esac

root=$(cd "$(dirname "$0")/.." && pwd)
go_image=${GO_IMAGE:-golang:1.26.9}
ps_image=${PS_IMAGE:-mcr.microsoft.com/powershell:ubuntu-24.04}
vol="aulaglobal-install-test-$$"

docker volume create "$vol" >/dev/null
trap 'docker volume rm -f "$vol" >/dev/null 2>&1 || true' EXIT INT TERM

# Etapa 1: compila, crea y firma las versiones falsas y (salvo en modo ps) prueba install.sh.
prepare=0
[ "$what" = ps ] && prepare=1
status=0
set -- docker run --rm -v "$root":/src:ro -v "$vol":/work -e PREPARE_ONLY="$prepare" -w /src
if [ -n "${GOCACHE_VOLUME:-}" ]; then
  set -- "$@" -v "$GOCACHE_VOLUME":/gocache -e GOCACHE=/gocache/build -e GOMODCACHE=/gocache/mod
fi
"$@" "$go_image" bash /src/scripts/test-install/inner.sh || status=1

# Etapa 2: install.ps1 bajo PowerShell 7 (Linux).
if [ "$what" != sh ] && { [ "$what" = ps ] || [ "$status" = 0 ]; }; then
  docker run --rm -v "$root":/src:ro -v "$vol":/work \
    "$ps_image" pwsh -NoProfile -File /src/scripts/test-install/inner.ps1 || status=1
fi
exit "$status"
