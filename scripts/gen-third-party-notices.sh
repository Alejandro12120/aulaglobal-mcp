#!/bin/sh
# Regenerates THIRD_PARTY_NOTICES.md from the module cache. Needs only Docker.
# The Go image tag is read from go.mod so the standard-library notice matches the
# toolchain that builds the releases. Files are written as the invoking user.
#
#   scripts/gen-third-party-notices.sh
#
# Edit scripts/gen-notices/preamble.md (AGDownloader section) by hand; the Go
# standard library and module sections are generated. A new dependency whose
# licence is not in scripts/gen-notices/main.go (spdxOf) stops the run: read its
# LICENSE and add it there.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
go_version=$(sed -n 's/^go \([0-9][0-9.]*\)$/\1/p' "$root/go.mod")
[ -n "$go_version" ] || { echo "cannot read the Go version from go.mod" >&2; exit 1; }

# GOFLAGS=-mod=readonly: never touch go.mod/go.sum. Caches live in tmpfs so the
# host is left clean; the modules are downloaded again on each run.
docker run --rm \
  --user "$(id -u):$(id -g)" \
  -e HOME=/tmp/home -e GOPATH=/tmp/home/go -e GOCACHE=/tmp/home/cache \
  -e GOFLAGS=-mod=readonly -e GOTOOLCHAIN=local \
  --tmpfs /tmp/home:exec,mode=1777,size=1g \
  -v "$root":/src -w /src \
  "golang:$go_version" \
  go run ./scripts/gen-notices -o THIRD_PARTY_NOTICES.md

echo "THIRD_PARTY_NOTICES.md regenerated; review 'git diff' before committing." >&2
