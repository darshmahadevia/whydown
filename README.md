# Whydown

Whydown explains Docker container failures in plain English. It collects logs, exit status, OOM flags, health checks, environment settings, networks, and current resource usage. Groq or Google turns the sanitized evidence into a short report with a likely cause, supporting facts, and suggested fixes.

Whydown never executes a suggested fix.

## Build and diagnose a container

Install Go 1.26.5 or newer and start Docker Engine or Docker Desktop.

```sh
make build
./bin/whydown list
export GROQ_API_KEY='your-key'
./bin/whydown diagnose web
./bin/whydown watch --provider groq --json
```

To use Google, set `GOOGLE_API_KEY` or `GEMINI_API_KEY`.

```sh
./bin/whydown diagnose --provider google web
./bin/whydown diagnose --provider google --model YOUR_MODEL web
```

Flags follow the command and precede the container name. `WHYDOWN_PROVIDER` and `WHYDOWN_MODEL` set defaults. The default provider is Groq. `.env` files are not loaded automatically.

To try the deterministic baseline without an API key, select `offline` explicitly.

```sh
./bin/whydown diagnose --provider offline web
./bin/whydown inspect web
```

`inspect` prints sanitized evidence. `diagnose --json` prints a structured report. Offline reports use fixed rules and have fewer diagnostic abilities than AI reports. Provider errors never silently select the offline baseline.

## Connect an assistant

Add a stdio MCP server to your assistant's configuration. Use the absolute path of the binary you built.

```json
{
  "mcpServers": {
    "whydown": {
      "command": "/absolute/path/to/Whydown/bin/whydown",
      "args": ["mcp", "--provider", "groq"],
      "env": {"GROQ_API_KEY": "YOUR_KEY"}
    }
  }
}
```

The server supports MCP revision `2025-11-25` over newline-delimited stdio. It exposes `list_containers`, `inspect_container`, and `diagnose_container`. Inspect and diagnose accept a `container` name or ID. Tool results contain text with JSON data. Standard output contains only protocol messages.

Use `--provider offline` for local assistant testing without an API key. The assistant can retrieve sanitized evidence and reason about it using its own model.

## Verify diagnoses

```sh
make verify
make eval
```

The included eight fixtures cover memory exhaustion, a localhost database address, an unresolvable dependency, permissions, occupied ports, health checks, generic process failure, and insufficient evidence.

`eval` runs every case and checks that every expected term appears in the summary, cause, or fixes. Errors count as failed cases. A failed evaluation exits with status 1. This is a lexical smoke test. It does not grade factual accuracy, supporting evidence quality, or whether a proposed fix works.

The offline fixture score measures deterministic rule coverage. It is not AI accuracy and does not prove live fault detection. To evaluate a provider on the same inputs, run the following command with its API key set.

```sh
./bin/whydown eval --provider groq --cases testdata/failures.json
```

To create real failures and collect real evidence, run the following command on a Docker host. The script pulls Python and BusyBox images. It starts four uniquely named containers, waits for each failure, saves sanitized cases, and removes only those containers in its cleanup block.

```sh
make live-eval
python3 scripts/live-eval.py --provider google
```

The script requires Python 3. It writes `artifacts/live-cases.json`. The default provider is offline. It tests collection and diagnosis of four faults, not the event watcher. Docker and live provider calls were unavailable during initial implementation, so no live accuracy result is claimed.

## Understand collection and privacy

The Docker client uses the official Moby Go SDK and Docker environment configuration, including `DOCKER_HOST` and TLS variables. Container evidence has the following limits.

- Logs include at most 200 recent lines and 64 KiB of decoded text.
- Environment and command metadata have item and byte limits.
- Health history includes at most five entries.
- Docker operations have deadlines. Live stats have a two-second deadline.
- Stopped containers have no current CPU or memory sample. A warning records that limitation.

Collection sanitizes complete metadata and decoded logs before applying output limits. Separate sensitive command flags and their values are handled together. Incomplete or oversized log responses are omitted with a warning so transport truncation cannot expose a credential fragment. The AI client sanitizes again immediately before each request. Redaction removes unknown environment values, URL credentials, common credential assignments, bearer tokens, recognized API key formats, and private key blocks. Connection URL hosts remain available for diagnosis. AI credentials are also excluded from evidence sent to the provider.

Redaction is best effort. Unlabelled secrets that are absent from configuration, encoded secrets, and sensitive business data can remain in logs. Inspect sanitized evidence before using a provider for sensitive workloads. Evidence stays in memory unless you redirect output or run the live evaluation script. Normal provider data handling still applies to submitted evidence.

The watcher scans existing failures, including Docker startup errors, listens for failure events, and checks restart counts every 15 seconds. Three restarts within a minute trigger a loop report. The CLI reconnects after a Docker stream failure. Reports can repeat across events or reconnections. A container that stops responding is detected through its Docker health check. Whydown does not probe arbitrary application endpoints.

Provider requests have a 30-second timeout, request and response size limits, and JSON field validation. Logs are marked as untrusted evidence in the AI prompt. A valid report can still be wrong. Fixes remain advice for human review.

## Maintain the code

`internal/domain` defines evidence and report records. `internal/docker` owns SDK collection and monitoring. `internal/redact` owns privacy filtering. `internal/ai` owns provider calls and offline rules. `internal/mcp` owns assistant transport. `internal/eval` owns scoring. `cmd/whydown` connects configuration and output.

See [the implementation plan](docs/plan.md) and [decision trail](docs/decisions.tsv).
