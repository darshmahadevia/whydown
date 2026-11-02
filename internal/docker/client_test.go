package docker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
	"whydown/internal/domain"
)

func fakeClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/v1.56")
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	api, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.56"))
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{api: api}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
func sendJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func fixture(id, state string) container.InspectResponse {
	return container.InspectResponse{ID: id, Name: "/" + id, Image: "sha256:abc", Path: "sh", Args: []string{"-c", "run"}, State: &container.State{Status: container.ContainerState(state), Running: state == "running", ExitCode: 137, OOMKilled: state == "exited", Health: &container.Health{Status: container.Unhealthy, Log: []*container.HealthcheckResult{{ExitCode: 1, Output: "connection refused"}}}}, RestartCount: 4, Config: &container.Config{Image: "example:latest", Env: []string{"TOKEN=secret"}}}
}

func TestNewUsesDockerEnvironment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.56")
			_, _ = io.WriteString(w, "OK")
			return
		}
		if r.URL.Path != "/v1.56/containers/json" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("all") != "1" {
			t.Error("stopped containers excluded")
		}
		sendJSON(w, []container.Summary{{ID: "abc", Names: []string{"/app"}, State: "exited", Status: "Exited (1)"}})
	}))
	defer server.Close()
	t.Setenv("DOCKER_HOST", server.URL)
	t.Setenv("DOCKER_API_VERSION", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "app" || got[0].State != "exited" {
		t.Fatalf("containers = %+v", got)
	}
}

func TestCollectDemuxAndStats(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/containers/app/json":
			sendJSON(w, fixture("app", "running"))
		case "/containers/app/logs":
			if r.URL.Query().Get("tail") != "200" || r.URL.Query().Get("follow") == "1" {
				t.Error("logs must be bounded and non-following")
			}
			writeFrame(w, 1, "starting\n")
			writeFrame(w, 2, "fatal\n")
		case "/containers/app/stats":
			if r.URL.Query().Get("stream") != "false" {
				t.Error("stats streaming enabled")
			}
			sendJSON(w, map[string]any{"memory_stats": map[string]uint64{"usage": 42, "limit": 1024}, "cpu_stats": map[string]any{"cpu_usage": map[string]int{"total_usage": 300}, "system_cpu_usage": 2000, "online_cpus": 2}, "precpu_stats": map[string]any{"cpu_usage": map[string]int{"total_usage": 100}, "system_cpu_usage": 1000}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	e, err := c.Collect(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if e.Logs != "starting\nfatal\n" || e.CPUPercent != 40 || e.MemoryUsage != 42 || e.MemoryLimit != 1024 {
		t.Fatalf("evidence = %+v", e)
	}
	if e.Container.Name != "app" || e.Container.Image != "example:latest" || e.Env[0] != "TOKEN=[REDACTED]" || e.Health != "unhealthy" || e.Command[0] != "sh" {
		t.Fatalf("metadata = %+v", e)
	}
}

func TestCollectBoundsTTYAndMetadata(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/json") {
			info := fixture("app", "exited")
			info.Config.Tty = true
			info.HostConfig = &container.HostConfig{Resources: container.Resources{Memory: 8192}}
			info.Config.Env = make([]string, 500)
			for i := range info.Config.Env {
				info.Config.Env[i] = strings.Repeat("x", 10000)
			}
			for range 20 {
				info.State.Health.Log = append(info.State.Health.Log, &container.HealthcheckResult{Output: strings.Repeat("h", 10000)})
			}
			sendJSON(w, info)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/logs") {
			_, _ = io.WriteString(w, strings.Repeat("x", maxLogWireBytes-1))
			return
		}
		t.Errorf("stopped container requested %s", r.URL.Path)
	})
	e, err := c.Collect(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if e.MemoryLimit != 8192 || len(e.Logs) != maxLogBytes || len(strings.Join(e.Env, "")) > maxFieldBytes || len(e.HealthLogs) != 5 || len(e.Warnings) == 0 {
		t.Fatalf("bounds failed, logs %d env %d health %d warnings %v", len(e.Logs), len(strings.Join(e.Env, "")), len(e.HealthLogs), e.Warnings)
	}
}

func TestCollectStatsTimeoutAndCancellation(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelParent), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/json"):
					sendJSON(w, fixture("app", "running"))
				case strings.HasSuffix(r.URL.Path, "/logs"):
					w.WriteHeader(http.StatusOK)
				case strings.HasSuffix(r.URL.Path, "/stats"):
					if cancelParent {
						cancel()
					}
					<-r.Context().Done()
				}
			})
			e, err := c.Collect(ctx, "app")
			if cancelParent {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || !strings.Contains(strings.Join(e.Warnings, " "), "Live stats unavailable") {
				t.Fatalf("stats should degrade, evidence %+v error %v", e, err)
			}
		})
	}
}

func TestCollectRejectsHugeLogFrameAllocation(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/json") {
			sendJSON(w, fixture("app", "exited"))
			return
		}
		header := []byte{1, 0, 0, 0, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(header[4:], ^uint32(0))
		_, _ = w.Write(append(header, []byte("last log")...))
	})
	e, err := c.Collect(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if e.Logs != "" || !strings.Contains(strings.Join(e.Warnings, " "), "omitted") {
		t.Fatalf("logs = %q", e.Logs)
	}
}

func TestMissingContainer(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		sendJSON(w, map[string]string{"message": "No such container"})
	})
	_, err := c.Collect(context.Background(), "gone")
	if !gone(err) {
		t.Fatalf("error = %v", err)
	}
}

func TestWatchInitialFailuresAndCallbackError(t *testing.T) {
	sentinel := errors.New("callback stopped")
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events":
			w.Header().Set("Content-Type", "application/json")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/containers/json":
			sendJSON(w, []container.Summary{{ID: "failed"}, {ID: "success"}})
		case "/containers/failed/json":
			sendJSON(w, fixture("failed", "exited"))
		case "/containers/failed/logs":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	err := c.Watch(context.Background(), func(e domain.Evidence) error {
		if e.Container.ID != "failed" || !e.OOMKilled {
			t.Errorf("evidence %+v", e)
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v", err)
	}
}

func TestWatchEventsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var delivered atomic.Int32
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/events":
			w.Header().Set("Content-Type", "application/json")
			w.(http.Flusher).Flush()
			for _, action := range []events.Action{events.ActionHealthStatusHealthy, events.ActionDie, events.ActionOOM, events.ActionHealthStatusUnhealthy, events.ActionRestart, events.ActionRestart, events.ActionRestart} {
				_ = json.NewEncoder(w).Encode(events.Message{Type: events.ContainerEventType, Action: action, Actor: events.Actor{ID: "app", Attributes: map[string]string{"exitCode": "23"}}})
				w.(http.Flusher).Flush()
			}
			<-r.Context().Done()
		case r.URL.Path == "/containers/json":
			sendJSON(w, []container.Summary{})
		case strings.HasSuffix(r.URL.Path, "/json"):
			sendJSON(w, fixture("app", "exited"))
		case strings.HasSuffix(r.URL.Path, "/logs"):
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	err := c.Watch(ctx, func(e domain.Evidence) error {
		n := delivered.Add(1)
		if n == 1 && e.ExitCode != 23 {
			t.Errorf("die exit code = %d", e.ExitCode)
		}
		if n == 4 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || delivered.Load() != 4 {
		t.Fatalf("error %v delivered %d", err, delivered.Load())
	}
}

func TestWatchStreamEOF(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			w.WriteHeader(http.StatusOK)
			return
		}
		sendJSON(w, []container.Summary{})
	})
	err := c.Watch(context.Background(), func(domain.Evidence) error { return nil })
	if !errors.Is(err, io.EOF) {
		t.Fatalf("error = %v", err)
	}
}

func TestScanDetectsRestartCountIncreaseAndDeduplicates(t *testing.T) {
	var restarts atomic.Int32
	restarts.Store(40)
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/containers/json":
			sendJSON(w, []container.Summary{{ID: "app"}})
		case "/containers/app/json":
			info := fixture("app", "running")
			info.State.Health = nil
			info.State.OOMKilled = false
			info.RestartCount = int(restarts.Load())
			sendJSON(w, info)
		case "/containers/app/logs":
			w.WriteHeader(http.StatusOK)
		case "/containers/app/stats":
			sendJSON(w, container.StatsResponse{})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	seen := make(map[string]observation)
	reports := 0
	consume := func(domain.Evidence) error { reports++; return nil }
	for _, count := range []int32{40, 41, 43, 43} {
		restarts.Store(count)
		if err := c.scan(context.Background(), seen, consume); err != nil {
			t.Fatal(err)
		}
	}
	if reports != 1 {
		t.Fatalf("reports = %d, expected only the new restart loop", reports)
	}
}

func TestWatchCancelsIdleStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		sendJSON(w, []container.Summary{})
		cancel()
	})
	done := make(chan error, 1)
	go func() { done <- c.Watch(ctx, func(domain.Evidence) error { return nil }) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not cancel")
	}
}

func writeFrame(w io.Writer, stream byte, value string) {
	header := []byte{stream, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(header[4:], uint32(len(value)))
	_, _ = w.Write(append(header, []byte(value)...))
}

func TestScanInitialFailuresSkipsSuccessfulExitsAndDisappearedContainers(t *testing.T) {
	states := map[string]string{"failed": "exited", "success": "exited", "restart": "restarting", "unhealthy": "running", "healthy": "running", "dead": "dead"}
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			sendJSON(w, []container.Summary{{ID: "failed"}, {ID: "success"}, {ID: "restart"}, {ID: "unhealthy"}, {ID: "healthy"}, {ID: "dead"}, {ID: "removed"}})
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		id := parts[2]
		if id == "removed" {
			w.WriteHeader(404)
			sendJSON(w, map[string]string{"message": "No such container"})
			return
		}
		switch parts[3] {
		case "json":
			info := fixture(id, states[id])
			info.State.Health = nil
			info.State.OOMKilled = false
			if id == "success" {
				info.State.ExitCode = 0
			}
			if id == "unhealthy" {
				info.State.Health = &container.Health{Status: container.Unhealthy}
			}
			sendJSON(w, info)
		case "logs":
			http.Error(w, "logging driver unavailable", 500)
		case "stats":
			http.Error(w, "stats unavailable", 500)
		}
	})
	seen := make(map[string]observation)
	got := make(map[string]int)
	for range 2 {
		if err := c.scan(context.Background(), seen, func(e domain.Evidence) error { got[e.Container.ID]++; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 4 || got["failed"] != 1 || got["restart"] != 1 || got["unhealthy"] != 1 || got["dead"] != 1 {
		t.Fatalf("reports = %v", got)
	}
}

func TestCollectRedactsSecretsDefinedBeyondMetadataLimit(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/json") {
			info := fixture("app", "exited")
			info.Config.Env = make([]string, 150)
			for i := range info.Config.Env {
				info.Config.Env[i] = "PATH=/usr/bin"
			}
			info.Config.Env = append(info.Config.Env, "CUSTOM_CREDENTIAL=hidden-value-without-label")
			sendJSON(w, info)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/logs") {
			writeFrame(w, 1, "Failed login hidden-value-without-label")
			return
		}
		http.NotFound(w, r)
	})
	e, err := c.Collect(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.Logs, "hidden-value-without-label") || !strings.Contains(e.Logs, "[REDACTED]") {
		t.Fatalf("credential leaked: %s", e.Logs)
	}
	if len(e.Env) > maxFieldItems {
		t.Fatal("env limit exceeded")
	}
}

func TestCollectSanitizesWholeEvidenceBeforeClipping(t *testing.T) {
	secret := strings.Repeat("q", 5000)
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/json"):
			info := fixture("app", "exited")
			info.Config.Env = []string{"CUSTOM_CREDENTIAL=" + secret}
			info.Args = []string{secret, "--password", "flag-only-secret"}
			info.State.Error = secret
			info.State.Health.Log = []*container.HealthcheckResult{{Output: secret}}
			sendJSON(w, info)
		case strings.HasSuffix(r.URL.Path, "/logs"):
			writeFrame(w, 1, strings.Repeat("a", maxLogBytes-2000)+secret+" flag-only-secret")
		default:
			http.NotFound(w, r)
		}
	})
	evidence, err := c.Collect(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(evidence)
	if strings.Contains(string(data), strings.Repeat("q", 100)) || strings.Contains(string(data), "flag-only-secret") {
		t.Fatalf("partial credential leaked: %s", data)
	}
	if len(evidence.Logs) > maxLogBytes {
		t.Fatal("log limit exceeded")
	}
}

func TestCollectOmitsLogsCutAtWireLimit(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/json") {
			info := fixture("app", "exited")
			info.Config.Tty = true
			sendJSON(w, info)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/logs") {
			_, _ = io.WriteString(w, strings.Repeat("x", maxLogWireBytes+1))
			return
		}
		http.NotFound(w, r)
	})
	evidence, err := c.Collect(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Logs != "" || !strings.Contains(strings.Join(evidence.Warnings, " "), "omitted") {
		t.Fatalf("unsafe truncated log response: %#v", evidence)
	}
}

func TestScanDetectsStartupErrorAndDeduplicates(t *testing.T) {
	stateError := "Bind failed: port is already allocated"
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/containers/json":
			sendJSON(w, []container.Summary{{ID: "app", State: "created"}})
		case strings.HasSuffix(r.URL.Path, "/json"):
			info := fixture("app", "created")
			info.State.ExitCode = 0
			info.State.OOMKilled = false
			info.State.Health = nil
			info.State.Error = stateError
			sendJSON(w, info)
		case strings.HasSuffix(r.URL.Path, "/logs"):
			writeFrame(w, 1, "")
		default:
			http.NotFound(w, r)
		}
	})
	seen := make(map[string]observation)
	calls := 0
	consume := func(e domain.Evidence) error {
		calls++
		if e.Error != stateError {
			t.Fatalf("missing startup error: %#v", e)
		}
		return nil
	}
	for range 2 {
		if err := c.scan(context.Background(), seen, consume); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("expected one startup report, got %d", calls)
	}
	stateError = "different startup failure"
	if err := c.scan(context.Background(), seen, consume); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("changed startup error missed, calls %d", calls)
	}
}
