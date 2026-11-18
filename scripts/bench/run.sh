#!/usr/bin/env bash
# Run serially so revisions do not contend with one another.
set -euo pipefail

repo=$(git rev-parse --show-toplevel)
cd "$repo"
results="$repo/.bench-results"
image=golang:1.26.9
mkdir -p "$results"
temp=$(mktemp -d)
worktrees=()
cleanup() {
    for worktree in "${worktrees[@]}"; do
        git worktree remove --force "$worktree"
    done
    rm -rf "$temp"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker_args=(--rm -v gocache:/root/.cache/go-build -v gopath:/go -w /src)
docker run "${docker_args[@]}" -v "$repo:/src:ro" "$image" go mod download

# Select labels to rerun individual revisions. BENCH_RUNS and PROCESS_RUNS
# override sample counts. PROFILE=1 enables profiles for the measured revisions.
labels=("$@")
if (( ${#labels[@]} == 0 )); then
    labels=(before-refactor refactor main optimized)
fi

for label in "${labels[@]}"; do
    case "$label" in
        before-refactor) revision=927ab6c ;;
        refactor) revision=6f7bc19 ;;
        main) revision=0626e64 ;;
        optimized) revision=HEAD ;;
        *) printf 'Unknown label: %s\n' "$label" >&2; exit 1 ;;
    esac
    commit=$(git rev-parse "$revision^{commit}")
    printf '%s\t%s\n' "$label" "$commit"
    printf '%s\n' "$commit" > "$results/$label.commit"
    worktree="$temp/$label"
    git worktree add --detach "$worktree" "$commit"
    worktrees+=("$worktree")
    cp internal/mcpserver/bench_test.go "$worktree/internal/mcpserver/"
    cp internal/store/bench_test.go "$worktree/internal/store/"
    docker run "${docker_args[@]}" --network none \
        -v "$worktree:/src:ro" -v "$repo/scripts/bench:/bench:ro" -v "$results:/results" \
        -e BENCH_RUNS="${BENCH_RUNS:-10}" -e PROCESS_RUNS="${PROCESS_RUNS:-20}" \
        -e BENCH_TIME="${BENCH_TIME:-100ms}" -e PROFILE="${PROFILE:-0}" -e BENCH_LABEL="$label" \
        -e GOTOOLCHAIN=local -e GOPROXY=off -e GOSUMDB=off \
        "$image" sh /bench/measure.sh
done

docker run "${docker_args[@]}" --network none -v "$repo/scripts/bench:/bench:ro" -v "$results:/results" "$image" go run /bench/summarize.go /results
