package ai

import (
	"fmt"
	"net/url"
	"strings"

	"whydown/internal/domain"
	"whydown/internal/redact"
)

// Offline is a deterministic heuristic baseline, never a fallback for API errors.
func Offline(raw domain.Evidence) domain.Report {
	e := redact.Evidence(raw)
	r := domain.Report{Container: e.Container.Name, Provider: "offline", Confidence: "low", Cause: "unknown", Summary: "The available evidence does not identify a clear cause.", Fixes: []string{"Review the container's recent logs and configuration before making changes."}, Evidence: []string{fmt.Sprintf("Container state is %q; exit code is %d.", e.Container.State, e.ExitCode)}}
	if r.Container == "" {
		r.Container = e.Container.ID
	}
	text := strings.ToLower(e.Error + "\n" + e.Logs + "\n" + strings.Join(e.HealthLogs, "\n"))
	switch {
	case e.OOMKilled:
		r.Cause = "oom"
		r.Confidence = "high"
		r.Summary = "Docker reports that the container ran out of memory and was killed."
		r.Fixes = []string{"Check the application's memory use for leaks or spikes.", "Review the container memory limit and available host memory before increasing it."}
		r.Evidence = []string{"Docker reports OOMKilled=true."}
	case strings.Contains(text, "connection refused") || strings.Contains(text, "no such host") || strings.Contains(text, "could not translate host name"):
		r.Cause = "network"
		r.Confidence = "medium"
		r.Summary = "The application could not reach a service it needs."
		r.Fixes = []string{"Check the dependency hostname, port, readiness and Docker network membership."}
		r.Evidence = []string{"Logs or Docker errors report a refused connection or a hostname lookup failure."}

		for _, entry := range e.Env {
			key, value, ok := strings.Cut(entry, "=")
			if !ok || (key != "DATABASE_URL" && key != "DB_URL") {
				continue
			}
			u, err := url.Parse(value)
			if err == nil && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1") {
				r.Summary = "The application cannot reach its database, and its database URL points at this container's localhost."
				r.Fixes = []string{"If the database runs in another container, set the database URL hostname to that service's Docker network name and connect both containers to the same network.", "Confirm the database is ready and listening on the configured port."}
				r.Evidence = append(r.Evidence, key+" uses a loopback hostname.")
				break
			}
		}
	case strings.Contains(text, "permission denied"):
		r.Cause = "permission"
		r.Confidence = "medium"
		r.Summary = "The application was denied access to a file, directory or resource."
		r.Fixes = []string{"Check the container user and mounted file ownership and permissions."}
		r.Evidence = []string{"Logs or Docker errors contain permission denied."}
	case strings.Contains(text, "address already in use") || strings.Contains(text, "port is already allocated"):
		r.Cause = "port_conflict"
		r.Confidence = "medium"
		r.Summary = "A port the container needs is already in use."
		r.Fixes = []string{"Check which service owns the port, then choose an unused host port or stop the conflicting service after review."}
		r.Evidence = []string{"Logs or Docker errors report that a port is already in use."}
	case e.Health == "unhealthy":
		r.Cause = "unhealthy"
		r.Confidence = "high"
		r.Summary = "The container's health check is failing."
		r.Fixes = []string{"Review health check output and confirm that the check matches the application's port and startup time."}
		r.Evidence = []string{"Docker reports health status unhealthy."}
	case e.ExitCode != 0:
		r.Cause = "exit"
		r.Confidence = "medium"
		r.Summary = fmt.Sprintf("The container process exited with error code %d.", e.ExitCode)
		r.Fixes = []string{"Review application startup logs, required environment variables and the configured command."}
	}
	return r
}
