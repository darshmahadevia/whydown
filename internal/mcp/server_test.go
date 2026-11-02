package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"whydown/internal/domain"
)

type fakeDocker struct{ calls int }

func (f *fakeDocker) List(context.Context) ([]domain.Container, error) {
	return []domain.Container{{Name: "web"}}, nil
}
func (f *fakeDocker) Collect(_ context.Context, id string) (domain.Evidence, error) {
	f.calls++
	return domain.Evidence{Container: domain.Container{Name: id}, Logs: "private"}, nil
}
func TestStdioLifecycleAndTools(t *testing.T) {
	f := &fakeDocker{}
	s := Server{Docker: f, Sanitize: func(e domain.Evidence) domain.Evidence { e.Logs = "[REDACTED]"; return e }, Diagnose: func(_ context.Context, e domain.Evidence) (domain.Report, error) {
		return domain.Report{Summary: "Database is unreachable"}, nil
	}}
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"inspect_container","arguments":{"container":"web"}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"diagnose_container","arguments":{"container":"web"}}}
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"inspect_container","arguments":{}}}
`
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 || f.calls != 2 {
		t.Fatalf("responses %d calls %d: %s", len(lines), f.calls, out.String())
	}
	if strings.Contains(out.String(), "private") || !strings.Contains(out.String(), "[REDACTED]") || !strings.Contains(out.String(), "Database is unreachable") {
		t.Fatal(out.String())
	}
	var r response
	if err := json.Unmarshal([]byte(lines[4]), &r); err != nil || r.Error == nil || r.Error.Code != -32602 {
		t.Fatalf("invalid missing argument response %s", lines[4])
	}
}
func TestMalformedInputRecovers(t *testing.T) {
	var out bytes.Buffer
	if err := (Server{}).Serve(context.Background(), strings.NewReader("oops\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"ping\"}\n"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "-32700") || !strings.Contains(out.String(), `"id":2`) {
		t.Fatal(out.String())
	}
}

func TestCancellationWithOpenInput(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (Server{}).Serve(ctx, reader, io.Discard) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("MCP did not cancel")
	}
}
