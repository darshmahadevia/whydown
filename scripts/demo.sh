#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
demo_provider="${WHYDOWN_DEMO_PROVIDER:-offline}"
demo_container="whydown-demo-oom-$$"
export DOCKER_CLI_HINTS=false
test -x bin/whydown || { echo 'Run make build first.' >&2; exit 1; }
docker info >/dev/null
docker image inspect python:3.12-alpine >/dev/null
demo_dir="$(mktemp -d)"
trap 'if [ -s "$demo_dir/container-id" ]; then docker rm -f "$(cat "$demo_dir/container-id")" >/dev/null; fi; rm -f "$demo_dir/container-id"; rmdir "$demo_dir"' EXIT

printf '\033[2J\033[H\033[1;36mWhydown\033[0m\n'
printf 'A real Docker failure, explained from runtime evidence.\n'
printf 'Provider: %s | Suggested fixes are advice only.\n\n' "$demo_provider"
sleep 3
printf '\033[1;32m$\033[0m docker run --name %s --memory 32m --memory-swap 32m \\\n' "$demo_container"
printf '    python:3.12-alpine python -c '\''x = bytearray(256*1024*1024)'\''\n'
sleep 2
set +e
docker run --cidfile "$demo_dir/container-id" --name "$demo_container" --memory 32m --memory-swap 32m \
    python:3.12-alpine python -c 'x = bytearray(256*1024*1024)'
demo_exit=$?
set -e
if [ "$demo_exit" -ne 137 ]; then
    echo "Expected OOM exit 137, got $demo_exit. Check the demo container." >&2
    exit 1
fi
printf '\nContainer exited with code %s.\n\n' "$demo_exit"
sleep 2
printf '\033[1;32m$\033[0m docker inspect --format '\''Exit={{.State.ExitCode}} OOMKilled={{.State.OOMKilled}}'\'' %s\n' "$demo_container"
docker inspect --format 'Exit={{.State.ExitCode}} OOMKilled={{.State.OOMKilled}}' "$demo_container"
test "$(docker inspect --format '{{.State.OOMKilled}}' "$demo_container")" = true
sleep 5

printf '\033[2J\033[H\033[1;36mWhydown / Diagnose\033[0m\n\n'
printf '\033[1;32m$\033[0m ./bin/whydown diagnose --provider %s %s\n' "$demo_provider" "$demo_container"
sleep 2
./bin/whydown diagnose --provider "$demo_provider" "$demo_container"
sleep 13
printf '\n\033[1;36mEvidence collected. Fixes suggested. No fixes executed.\033[0m\n'
sleep 4
