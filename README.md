# Whydown

Whydown explains why Docker containers fail. It collects logs, exit status, OOM flags, health checks, configuration, networks, and live resource usage. Groq or Google turns that evidence into a report with a likely cause, supporting facts, confidence, and suggested fixes.

Use it from the terminal or connect an assistant through its read-only MCP server. Whydown never executes a suggested fix.

## Get started

You need Go 1.26.5 or newer, Make, and access to a running Docker Engine or Docker Desktop. Run these commands from the repository root.

Build the binary and list your containers, including stopped containers.

```sh
make build
./bin/whydown list
```

Set `GROQ_API_KEY` in your shell, then diagnose a container. Replace `web` with a container name or ID from the list.

```sh
./bin/whydown diagnose web
```

The report prints a summary, likely cause, confidence, suggested fixes, and supporting evidence. To try diagnosis without an API key, select the offline provider.

```sh
./bin/whydown diagnose --provider offline web
```

Offline mode uses fixed rules with fewer diagnostic abilities than the AI providers. AI errors never silently fall back to offline mode.

## Use the CLI

Flags follow the command and precede the container name or ID.

```sh
./bin/whydown inspect web
./bin/whydown diagnose --json web
./bin/whydown watch --provider groq --json
./bin/whydown help
```

`list` and `inspect` always print JSON and need no API key. `inspect` prints sanitized evidence so you can review what diagnosis uses. `diagnose` and `watch` print readable reports by default. Add `--json` for structured reports. In watch mode, each JSON report occupies one line.

The watcher scans existing failures, listens for Docker failure events, and scans again every 15 seconds. It detects startup errors, unhealthy containers, and restart loops of at least three restarts within a minute. It retries a disconnected Docker stream after five seconds. Press Ctrl+C to stop it.

Reports can repeat across events or reconnections. The watcher monitors all containers on the connected Docker host. It detects an unresponsive application through its Docker health check, so configure a health check for that purpose. Whydown does not probe application endpoints itself.

## Configure a provider

Groq is the default. To use Google, set `GOOGLE_API_KEY` or `GEMINI_API_KEY`, then select `google`.

```sh
./bin/whydown diagnose --provider google web
./bin/whydown diagnose --provider google --model YOUR_MODEL web
```

The following environment variables set credentials and defaults. Command flags override the provider and model defaults.

| Variable | Purpose |
| --- | --- |
| `WHYDOWN_PROVIDER` | Default provider, `groq`, `google`, or `offline`. Defaults to `groq`. |
| `WHYDOWN_MODEL` | Model name. The code defaults to `openai/gpt-oss-20b` for Groq and `gemini-3.5-flash-lite` for Google. |
| `GROQ_API_KEY` | Groq credential. |
| `GOOGLE_API_KEY` | Google credential. Takes precedence over `GEMINI_API_KEY`. |
| `GEMINI_API_KEY` | Alternative Google credential. |
| `DOCKER_HOST` | Docker Engine address. |
| `DOCKER_TLS_VERIFY` | Docker TLS verification setting. |
| `DOCKER_CERT_PATH` | Directory with Docker TLS certificates. |

[`.env.example`](.env.example) lists the provider settings. Whydown does not load `.env` files automatically. If you keep credentials in a local `.env`, export them when you load it so the binary inherits them.

```sh
set -a
source .env
set +a
```

Git ignores `.env` files. Restrict access to a credential file with `chmod 600 .env`.

## Connect an assistant

Add a stdio MCP server to your assistant's configuration. Replace the command with the absolute path to your built binary and supply your provider credential.

```json
{
	"mcpServers": {
		"whydown": {
			"command": "/absolute/path/to/Whydown/bin/whydown",
			"args": ["mcp", "--provider", "groq"],
			"env": {
				"GROQ_API_KEY": "YOUR_KEY"
			}
		}
	}
}
```

For local use without an API key, change the provider to `offline` and omit `env`. An assistant can call `inspect_container` and reason about the evidence with its own model.

The server exposes these tools.

| Tool | Input | Result |
| --- | --- | --- |
| `list_containers` | No arguments. | All containers on the connected Docker host. |
| `inspect_container` | `container`, a name or ID. | Sanitized evidence. |
| `diagnose_container` | `container`, a name or ID. | A diagnosis from the configured provider. |

The server uses MCP revision `2025-11-25` over newline-delimited stdio. Tool results contain text with JSON data. Standard output contains only protocol messages.

## Understand what leaves your machine

With Groq or Google selected, diagnosis sends sanitized container evidence to that provider. Offline diagnosis and `inspect` need no AI provider calls.

Whydown sanitizes metadata and decoded logs before applying output limits. It masks unknown environment values, URL credentials, sensitive command arguments, common credential assignments, bearer tokens, recognized API key formats, and private key blocks. Connection URL hosts remain available for diagnosis. The AI client sanitizes evidence again before each request and removes its own API credential.

Redaction is best effort. Unlabelled secrets, encoded secrets, and sensitive business data can remain in logs. Review `inspect` output before submitting evidence from sensitive workloads. The provider's data handling policies still apply.

Collection has the following limits.

- Logs include at most 200 recent lines and 64 KiB of decoded text. Incomplete or oversized transport responses are omitted with a warning.
- Environment, command, and network metadata have item and byte limits. Health history includes at most five entries.
- Docker requests have deadlines. Live stats have a two-second deadline. Stopped containers have no current CPU or memory sample, which the evidence records as a warning.

Provider requests have a 30-second timeout, size limits, and JSON field validation. The prompt treats logs as untrusted evidence. A valid report can still be wrong. Review suggested fixes before acting on them.

Normal collection and diagnosis keep evidence in memory unless you redirect output. The verification scripts below save artifacts to disk.

## Verify a change

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

See [recorded verification results](docs/verification.md) for prior Docker and Groq runs, their limits, and checks that remain untested.

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

See the [implementation plan](docs/plan.md) and [decision trail](docs/decisions.tsv) for project context.
