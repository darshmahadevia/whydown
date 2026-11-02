# Develop Whydown

## Run local checks

Run the local checks from the repository root. These require Python 3 in addition to Go and Make.

```sh
make verify
make eval
```

`make verify` runs race tests, builds the binary, runs `go vet`, and checks the executable's offline evaluation, MCP discovery, and shutdown. These checks use simulated Docker and provider responses and require no running Docker host or API key.

`make eval` evaluates the offline rules against eight recorded fixtures. They cover memory exhaustion, a localhost database address, an unresolvable dependency, permissions, occupied ports, health checks, generic process failure, and insufficient evidence.

To evaluate an AI provider against the same fixtures, set its API key and run the following command.

```sh
./bin/whydown eval --provider groq --cases testdata/failures.json
```

The evaluator checks for expected terms in the summary, cause, or fixes. Provider errors and missing terms count as failures. Any failed case produces exit status 1. This lexical check measures fixture coverage. It does not establish diagnosis accuracy or prove that a suggested fix works.

### Run live Docker checks

These scripts require Python 3, the Docker CLI, and a running Docker host. They pull Python and BusyBox images, create containers with unique names, and remove their own containers in cleanup blocks. The default provider is offline.

```sh
make live-eval
make live-integration
```

`live-eval` creates five faults, collects real evidence, writes `artifacts/live-cases.json`, and evaluates the diagnoses. The faults are an OOM kill, a refused localhost connection, a permission error, an application port conflict, and a failing health check.

`live-integration` checks live resource collection, redaction, watcher behavior, MCP tools, and shutdown. It writes `artifacts/live-integration.json`.

To include provider calls, load your credentials and run a script with its provider flag.

```sh
python3 scripts/live-eval.py --provider google
python3 scripts/live-integration.py --provider groq
```

The integration script uses offline diagnosis for its watcher and the selected provider for MCP diagnosis. To test the complete Docker event to Groq report path, run `python3 scripts/live-watch-ai.py` with a Groq credential set. That optional check requires an empty Docker host to avoid submitting unrelated container logs.

See [recorded verification results](verification.md) for prior Docker and Groq runs, their limits, and checks that remain untested.

## Find the implementation

The code is organized by responsibility.

| Directory | Responsibility |
| --- | --- |
| `cmd/whydown` | CLI configuration, commands, and output. |
| `internal/domain` | Container evidence and diagnosis report types. |
| `internal/docker` | Docker SDK collection and monitoring. |
| `internal/redact` | Credential filtering. |
| `internal/ai` | Provider requests and offline rules. |
| `internal/mcp` | Assistant tools and stdio transport. |
| `internal/eval` | Fixture loading and scoring. |
| `scripts` | Executable smoke tests and live Docker checks. |

See the [implementation plan](plan.md) and [decision trail](decisions.tsv) for project context.

## Record the demo

The demo script creates a real container that exceeds its memory limit, confirms the OOM kill, and runs Whydown against it. It removes that container after diagnosis. You need the built binary, Docker, and the Python image.

```sh
make build
docker pull python:3.12-alpine
bash scripts/demo.sh
```

The script defaults to offline diagnosis. To use Groq, export your API key and set `WHYDOWN_DEMO_PROVIDER=groq`. The checked-in recording uses Groq. Its report is actual provider output, so another run may suggest different fixes.

To export a terminal recording, install asciinema, agg, and FFmpeg, then run the following commands from the repository root. Use a new output filename if a recording already exists.

```sh
asciinema rec --headless --return --window-size 112x38 \
	--output-format asciicast-v2 --title 'Whydown demo' \
	--command 'bash scripts/demo.sh' docs/assets/demo.cast
agg --renderer resvg --font-family 'Menlo,Arial' --theme github-dark --font-size 20 --line-height 1.3 --rows 26 \
	--idle-time-limit 20 --last-frame-duration 2 \
	docs/assets/demo.cast docs/assets/demo.gif
ffmpeg -i docs/assets/demo.gif -an -c:v libx264 -crf 18 \
	-pix_fmt yuv420p -vf 'fps=12,pad=ceil(iw/2)*2:ceil(ih/2)*2' \
	-movflags +faststart docs/assets/demo.mp4
```

The cast preserves terminal output and timing. The GIF and MP4 render that session without audio. No desktop windows or keyboard input are captured.
