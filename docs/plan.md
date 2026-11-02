# Whydown implementation plan

Whydown turns Docker failures into explanations and fixes without sending known secrets to an AI provider.

The completion gate is a runnable Go CLI, tested Docker evidence collection through the official SDK, tested outbound redaction, Groq and Google adapters, a cancellable watcher, MCP tools, and a reproducible failure evaluation command. Live Docker and live provider verification require their runtimes and credentials and are reported separately.

## Data and ownership

`domain.Evidence` holds bounded container facts. `domain.Report` holds a cause, summary, confidence, supporting facts, and suggested fixes. The Docker package owns collection. The AI package owns the only outbound diagnosis path and always redacts evidence. The command owns configuration, cancellation, and output. MCP uses the same collection and diagnosis path.

## Workflow

1. Compare a single service package with small packages around Docker, privacy, AI, and command boundaries. Select the latter because outbound redaction can be checked independently.
2. Build Docker collection on Qurvo and AI/privacy on Codex-Main. Jimit owns CLI, MCP, evaluation, and integration. Each account writes disjoint packages in the shared empty workspace.
3. At the throughput checkpoint, run package tests and review boundary contracts before adding integrations. Do not count fixtures as live fault injection.
4. Run tests with the race detector, vet, build, and exercise CLI and MCP through their actual transports.
5. Record limitations and runnable live failure recipes.

No automatic container changes are permitted. Diagnoses are advice only. Container logs are untrusted input. Deterministic fixture scores measure the offline baseline separately from AI provider accuracy.
