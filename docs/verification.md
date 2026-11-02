# Initial verification

Run `make verify` to repeat the local checks and executable smoke test. The local checks are `go test -race ./...`, `go vet ./...`, `make build`, and the built executable's offline evaluation. The built executable also accepts MCP initialize and tools/list messages over stdin and emits valid JSON-RPC replies with three tools. The executable smoke test also verifies clean SIGTERM shutdown while stdin remains open.

The offline evaluation passes eight of eight recorded fixtures. One deterministic run evaluates all eight inputs. The score is bounded by the fixed fixture set and lexical matching. It measures rule coverage, not AI accuracy, working fixes, or generalization to unseen failures. Provider errors and missing expected terms count as failures.

HTTP tests drive the Docker SDK against a fake Engine API. Provider tests capture outbound requests against local HTTP servers. These prove request wiring and handling of simulated inputs. They do not prove compatibility with a running Engine or provider account.

Live Docker verification is unavailable because neither the Docker executable nor a local Docker socket exists in this workspace. Provider calls were initially unavailable. Groq has since been connected and tested on the synthetic fixtures, as recorded below. `scripts/live-eval.py` supplies the runnable Docker fault experiments for a suitable host. No live Docker score is claimed.

An attempted DEV community search required an MLH connection and returned no articles. No community evidence was used in the design.

## Independent review corrections

The review reproduced three privacy defects and a missed startup-failure report. Regression tests now cover separate sensitive command arguments, URL-valued password assignments, and long known secrets in commands, errors, health output, and logs before output clipping. Provider HTTP tests check that command-only secrets do not reach either provider. Oversized or incomplete wire log responses are omitted rather than sanitized after transport truncation.

The watcher regression test drives the Docker SDK against a fake API. It checks that a container in created state with a startup error produces one report, a repeated scan does not duplicate it, and a changed startup error produces a new report.

## Groq connection check

Groq is configured locally with `openai/gpt-oss-120b`. The ignored `.env` has owner-only read and write permissions and must be loaded with `source .env` before CLI use. No credential is included in tracked files.

Two live runs submitted all eight synthetic fixtures. All requests returned validated reports. Both runs scored seven of eight under the existing lexical checker. The first run guessed a startup failure from missing evidence. The diagnosis prompt now explicitly treats missing fields as missing observations and requests unknown cause with low confidence. The second run followed that rule. Its failed lexical check was the localhost case, which identified the database connection problem and proposed a Docker network address but omitted the expected word hostname. This is a limitation of the lexical checker. These two runs do not establish general diagnosis accuracy.

Local reports are in `artifacts/groq-evaluation-initial.json` and `artifacts/groq-evaluation.json`.

## Live Docker verification on 2026-11-02

Docker Desktop supplied Engine 29.9.2 on Linux aarch64. The built application communicated with the real Engine through its Go SDK. Groq used the configured `openai/gpt-oss-120b` model.

The five live fault cases all passed the existing lexical checks. The inputs were actual evidence from a 32 MiB memory-limit kill, a refused connection to 127.0.0.1:5432, a permission-denied write, two HTTP servers attempting the same application port, and a failing Docker health check. Their generated summaries were inspected and matched those faults. This is one run over five controlled cases, not a general accuracy estimate.

`source .env` followed by `python3 scripts/live-eval.py --provider groq` repeats this run. The saved cases and reports are `artifacts/live-cases.json` and `artifacts/docker-groq-score.json`.

`python3 scripts/live-integration.py --provider groq` passed these nine checks.

- live CPU and memory collection.
- real command, environment and log credential redaction.
- watcher initial failure scan.
- watcher catches new crash events.
- restart loop evidence and monitoring.
- watcher detects unhealthy containers.
- watcher detects failed container startup in created state.
- MCP list, sanitized inspect, diagnosis and missing-container errors.
- live watcher clean shutdown.

Its saved reports are in `artifacts/live-integration.json`. The watcher used offline diagnosis to avoid sending unrelated container logs. Its MCP diagnosis used Groq. A separate `python3 scripts/live-watch-ai.py` run tested a real OOM event through the watcher and Groq together. It passed and saved `artifacts/live-ai-watch.json`. The script requires an empty Docker host to keep unrelated logs out of the provider requests.

A duplicate published-port test did not induce a failure on this Docker setup. Both containers started, and the second had no assigned published port in its network settings. The watcher startup-error test therefore uses a missing executable, which reliably leaves a created container with a real Docker state error. Application port conflicts are tested separately using two HTTP servers inside one container.

All containers created by these checks were removed. The final `make verify` passed race tests, vet, build, offline fixtures, MCP discovery and shutdown. Google remains covered by local HTTP tests rather than a live API call. Docker Desktop restart and reconnection during an active diagnosis were not exercised.
