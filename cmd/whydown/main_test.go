package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestOfflineEvaluationCommand(t *testing.T) {
	var out, stderr bytes.Buffer
	err := run(context.Background(), []string{"eval", "--provider", "offline", "--cases", "../../testdata/failures.json"}, strings.NewReader(""), &out, &stderr)
	if err != nil || !strings.Contains(out.String(), `"passed": 8`) {
		t.Fatalf("%v %s %s", err, out.String(), stderr.String())
	}
}
func TestRejectUnexpectedArguments(t *testing.T) {
	for _, args := range [][]string{{"inspect"}, {"list", "unexpected"}, {"diagnose", "a", "b"}, {"unknown"}} {
		var out bytes.Buffer
		if run(context.Background(), args, strings.NewReader(""), &out, &out) == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
