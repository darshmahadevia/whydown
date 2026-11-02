# Initial verification

Run `make verify` to repeat the local checks and executable smoke test. The local checks are `go test -race ./...`, `go vet ./...`, `make build`, and the built executable's offline evaluation. The built executable also accepts MCP initialize and tools/list messages over stdin and emits valid JSON-RPC replies with three tools. The executable smoke test also verifies clean SIGTERM shutdown while stdin remains open.

The offline evaluation passes eight of eight recorded fixtures. One deterministic run evaluates all eight inputs. The score is bounded by the fixed fixture set and lexical matching. It measures rule coverage, not AI accuracy, working fixes, or generalization to unseen failures. Provider errors and missing expected terms count as failures.

HTTP tests drive the Docker SDK against a fake Engine API. Provider tests capture outbound requests against local HTTP servers. These prove request wiring and handling of simulated inputs. They do not prove compatibility with a running Engine or provider account.

Live Docker verification is unavailable because neither the Docker executable nor a local Docker socket exists in this workspace. Live AI evaluation is unavailable because provider credentials have not been configured. `scripts/live-eval.py` supplies the runnable Docker fault experiments for a suitable host. No live score is claimed.

An attempted DEV community search required an MLH connection and returned no articles. No community evidence was used in the design.

## Independent review corrections

The review reproduced three privacy defects and a missed startup-failure report. Regression tests now cover separate sensitive command arguments, URL-valued password assignments, and long known secrets in commands, errors, health output, and logs before output clipping. Provider HTTP tests check that command-only secrets do not reach either provider. Oversized or incomplete wire log responses are omitted rather than sanitized after transport truncation.

The watcher regression test drives the Docker SDK against a fake API. It checks that a container in created state with a startup error produces one report, a repeated scan does not duplicate it, and a changed startup error produces a new report.
