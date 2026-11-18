#!/bin/sh
set -eu

export HOME
HOME=$(mktemp -d)
export XDG_CONFIG_HOME="$HOME/.config" XDG_CACHE_HOME="$HOME/.cache"
export GOCACHE=/root/.cache/go-build
out="/results/$BENCH_LABEL"

{
    go version
    uname -smr
    awk -F ': ' '/^model name/ { print $2; exit }' /proc/cpuinfo
    printf 'logical CPUs: '
    getconf _NPROCESSORS_ONLN
    printf 'Go samples: %s\nGo benchtime: %s\nProcess samples: %s\n' "$BENCH_RUNS" "$BENCH_TIME" "$PROCESS_RUNS"
} > "$out.environment.txt"

# Give batch downloads one timed iteration per sample so a run still measures
# the default limiter without spending minutes on each individual sample.
go test -buildvcs=false -c -o /tmp/mcpserver.test ./internal/mcpserver
go test -buildvcs=false -c -o /tmp/store.test ./internal/store
(
    cd /src/internal/mcpserver
    /tmp/mcpserver.test -test.run='^$' -test.bench='^BenchmarkMCP$/^(default_4rps|max_100rps)$/^(initialize|auth|list|download_5MiB|local)' \
        -test.benchmem -test.count="$BENCH_RUNS" -test.benchtime="$BENCH_TIME"
    /tmp/mcpserver.test -test.run='^$' -test.bench='^BenchmarkMCP$/^(default_4rps|max_100rps)$/^download_20_small$' \
        -test.benchmem -test.count="$BENCH_RUNS" -test.benchtime=1x
) > "$out.mcp.txt" 2>&1
if grep -q '^--- FAIL:' "$out.mcp.txt"; then
    cat "$out.mcp.txt" >&2
    exit 1
fi
(
    cd /src/internal/store
    /tmp/store.test -test.run='^$' -test.bench='^BenchmarkStore' -test.benchmem \
        -test.count="$BENCH_RUNS" -test.benchtime="$BENCH_TIME"
) > "$out.store.txt" 2>&1

CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' -o /tmp/aulaglobal-mcp ./cmd/aulaglobal-mcp
go build -buildvcs=false -o /tmp/process /bench/process.go
/tmp/process -binary /tmp/aulaglobal-mcp -runs "$PROCESS_RUNS" > "$out.process.tsv"

if [ "$PROFILE" = 1 ]; then
    (
        cd /src/internal/store
        /tmp/store.test -test.run='^$' -test.bench='^BenchmarkStoreSearch$/^literal_warm$' -test.benchtime=3s \
            -test.cpuprofile="$out.search.cpu" -test.memprofile="$out.search.mem"
    ) > "$out.search.profile.txt" 2>&1
    (
        cd /src/internal/mcpserver
        /tmp/mcpserver.test -test.run='^$' -test.bench='^BenchmarkMCP$/^max_100rps$/^download_20_small$' -test.benchtime=50x \
            -test.cpuprofile="$out.download.cpu" -test.memprofile="$out.download.mem"
    ) > "$out.download.profile.txt" 2>&1
    (
        cd /src/internal/mcpserver
        /tmp/mcpserver.test -test.run='^$' -test.bench='^BenchmarkMCP$/^default_4rps$/^initialize_tools_list$' -test.benchtime=3s \
            -test.cpuprofile="$out.initialize.cpu" -test.memprofile="$out.initialize.mem"
    ) > "$out.initialize.profile.txt" 2>&1
    for scenario in search download initialize; do
        case "$scenario" in
            search) binary=/tmp/store.test ;;
            *) binary=/tmp/mcpserver.test ;;
        esac
        go tool pprof -top "$binary" "$out.$scenario.cpu" > "$out.$scenario.cpu.top.txt"
        go tool pprof -top -alloc_space "$binary" "$out.$scenario.mem" > "$out.$scenario.alloc.top.txt"
    done
fi
