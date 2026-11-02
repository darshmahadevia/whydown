# Whydown

**Find out why your Docker container stopped.**

Whydown collects runtime evidence and asks Groq or Google Gemini to explain the failure. Each report includes a likely cause, confidence, supporting facts, and suggested fixes. Run it in your terminal or give an AI assistant access through MCP.

Whydown reads container state. It never executes a suggested fix.

## Demo

A Python container tries to allocate 256 MiB under a 32 MiB limit. Docker kills it, and Whydown uses the real container evidence to explain why.

[![Silent terminal recording of a Docker OOM failure and Whydown's Groq diagnosis](docs/assets/demo.gif)](docs/assets/demo.mp4)

[Watch the silent MP4](docs/assets/demo.mp4) · [Replay the terminal recording](docs/assets/demo.cast) · [Record your own](docs/development.md#record-the-demo)

The recording uses Groq. It shows actual command output, including the provider's suggested fixes. Those suggestions still need human review.

## What it does

- Diagnoses failures using logs, exit codes, OOM flags, health checks, configuration, networks, and live resource usage.
- Watches for crashes, unhealthy containers, startup errors, and restart loops.
- Returns readable reports or JSON for scripts.
- Gives assistants read-only tools to list, inspect, and diagnose containers.
- Offers an explicit offline mode with fixed diagnostic rules and no API key.

## Get started

You need Go 1.26.5 or newer, Make, and access to Docker Engine or Docker Desktop.

```sh
git clone https://github.com/darshmahadevia/whydown.git
cd whydown
make build
./bin/whydown list
```

Set your Groq API key, then replace `web` with a container name or ID from the list.

```sh
export GROQ_API_KEY='YOUR_GROQ_API_KEY'
./bin/whydown diagnose web
```

To try it without a provider account, use the offline rules.

```sh
./bin/whydown diagnose --provider offline web
```

Offline mode has fewer diagnostic abilities than the AI providers. Provider errors never silently switch to offline mode.

## Use the CLI

Place flags after the command and before the container name or ID.

| Command | What you get |
| --- | --- |
| `./bin/whydown list` | All containers, including stopped ones, as JSON. |
| `./bin/whydown inspect web` | Sanitized container evidence as JSON. No API key needed. |
| `./bin/whydown diagnose web` | A diagnosis from the configured provider. |
| `./bin/whydown diagnose --json web` | A structured diagnosis. |
| `./bin/whydown watch --json` | One JSON report per line as Docker failures occur. |
| `./bin/whydown help` | Commands and flags. |

The watcher monitors every container on the connected Docker host. It scans existing failures, listens for Docker events, and scans again every 15 seconds. Three restarts within a minute trigger a restart-loop report. It retries a disconnected Docker stream after five seconds. Press Ctrl+C to stop it.

Reports can repeat across events or reconnections. Configure Docker health checks to detect applications that stop responding. Whydown does not probe application endpoints itself.

## Choose a provider

Groq is the default. To use Google, export `GOOGLE_API_KEY` or `GEMINI_API_KEY` and select `google`.

```sh
export GOOGLE_API_KEY='YOUR_GOOGLE_API_KEY'
./bin/whydown diagnose --provider google web
./bin/whydown diagnose --provider google --model YOUR_MODEL web
```

Command flags override these environment defaults.

| Variable | Purpose |
| --- | --- |
| `WHYDOWN_PROVIDER` | `groq`, `google`, or `offline`. Defaults to `groq`. |
| `WHYDOWN_MODEL` | Model override. Code defaults are `openai/gpt-oss-20b` for Groq and `gemini-3.5-flash-lite` for Google. |
| `GROQ_API_KEY` | Groq credential. |
| `GOOGLE_API_KEY` | Google credential, preferred over `GEMINI_API_KEY`. |
| `GEMINI_API_KEY` | Alternative Google credential. |

Whydown uses the Docker SDK's environment configuration, including `DOCKER_HOST`, `DOCKER_TLS_VERIFY`, and `DOCKER_CERT_PATH`.

[`.env.example`](.env.example) lists the provider settings. Whydown does not load `.env` automatically. To load a local credential file and export its variables, run the following commands.

```sh
set -a
source .env
set +a
```

Git ignores `.env` files. Restrict access to your credential file with `chmod 600 .env`.

## Connect an AI assistant

Add Whydown to an assistant that supports stdio MCP servers. Replace the binary path and credential in this configuration.

```json
{
	"mcpServers": {
		"whydown": {
			"command": "/absolute/path/to/whydown/bin/whydown",
			"args": ["mcp", "--provider", "groq"],
			"env": {
				"GROQ_API_KEY": "YOUR_GROQ_API_KEY"
			}
		}
	}
}
```

The server exposes these read-only tools.

| Tool | Arguments | Result |
| --- | --- | --- |
| `list_containers` | None. | All containers on the connected Docker host. |
| `inspect_container` | `container`, a name or ID. | Sanitized evidence. |
| `diagnose_container` | `container`, a name or ID. | A report from the selected provider. |

For use without an API key, set the provider argument to `offline` and omit `env`. Your assistant can also call `inspect_container` and reason about the evidence with its own model.

The server supports MCP revision `2025-11-25` over newline-delimited stdio. Tool results contain text with JSON data. Standard output contains only protocol messages.

## Privacy and limits

Groq and Google receive sanitized container evidence during diagnosis. `inspect` and offline diagnosis make no AI provider calls.

Whydown redacts credentials before it limits evidence size, then sanitizes again before provider requests. It masks unknown environment values, URL credentials, sensitive command arguments, common credential assignments, bearer tokens, recognized API key formats, and private key blocks. Connection URL hosts remain available for diagnosis.

Redaction is best effort. Unlabelled or encoded secrets and sensitive business data can remain in logs. Review `inspect` output before submitting sensitive workloads. The provider's data handling policies apply to submitted evidence.

- Logs include at most 200 recent lines and 64 KiB of decoded text. Incomplete or oversized transport responses are omitted with a warning.
- Metadata has item and byte limits. Health history includes at most five entries.
- Stopped containers have no live CPU or memory sample. Collection records missing observations as warnings.
- Docker operations have deadlines, including two seconds for live stats. Provider requests have a 30-second timeout and size limits.

Reports can be wrong even when their JSON passes validation. Whydown keeps normal collection and diagnosis evidence in memory unless you redirect output. Verification scripts save artifacts to disk.

## Develop and verify

Run local verification with Go, Make, and Python 3. These checks need no Docker host or API key.

```sh
make verify
make eval
```

`make verify` runs race tests, vet, a build, and executable checks for offline evaluation, MCP discovery, and shutdown. `make eval` checks eight recorded failure cases against the offline rules. Its lexical score measures fixture coverage, not general diagnosis accuracy.

For live Docker experiments, provider evaluation, code organization, and demo recording instructions, see the [development guide](docs/development.md). Prior runs and their limits are in [verification results](docs/verification.md). The [implementation plan](docs/plan.md) and [decision trail](docs/decisions.tsv) describe project context.
