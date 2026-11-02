package redact_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"whydown/internal/domain"
	"whydown/internal/redact"
)

func TestEvidenceRedactsAcrossFieldsWithoutMutatingInput(t *testing.T) {
	e := domain.Evidence{
		Container:  domain.Container{Name: "api", Image: "private.registry/image"},
		Env:        []string{"PASSWORD=horse-battery", "CUSTOM_CREDENTIAL=opaque-value", "DATABASE_URL=postgres://alice:p%40ssword@db.internal:5432/shop?sslmode=require", "PORT=8080"},
		Logs:       `horse-battery opaque-value p@ssword alice Bearer bearer-abc123 password="log only secret" https://bob:url-only@api.internal/path?token=query-only`,
		Command:    []string{"app", "horse-battery", "log only secret", "query-only", "bearer-abc123", "url-only"},
		HealthLogs: []string{"horse-battery opaque-value"}, Error: "p@ssword", Warnings: []string{"url-only"}, Networks: []string{"bearer-abc123"},
	}
	before, _ := json.Marshal(e)
	safe := redact.Evidence(e)
	after, _ := json.Marshal(e)
	if string(before) != string(after) {
		t.Fatal("redaction mutated original evidence")
	}
	data, _ := json.Marshal(safe)
	for _, secret := range []string{"horse-battery", "opaque-value", "alice", "p@ssword", "p%40ssword", "bob", "url-only", "bearer-abc123", "log only secret", "query-only"} {
		if !strings.Contains(string(before), secret) {
			t.Fatalf("fixture does not include %q", secret)
		}
		if strings.Contains(string(data), secret) {
			t.Errorf("sanitized evidence contains %q: %s", secret, data)
		}
	}
	for _, clue := range []string{"db.internal:5432/shop", "sslmode=require", "PORT=8080", "api.internal/path"} {
		if !strings.Contains(string(data), clue) {
			t.Errorf("lost diagnostic clue %q: %s", clue, data)
		}
	}
	safe.Env[0] = "changed"
	safe.Command[0] = "changed"
	safe.HealthLogs[0] = "changed"
	if e.Env[0] != "PASSWORD=horse-battery" || e.Command[0] != "app" || e.HealthLogs[0] != "horse-battery opaque-value" {
		t.Fatal("output aliases input slices")
	}
}

func TestCredentialForms(t *testing.T) {
	for _, tc := range []struct{ name, text, secret string }{
		{"json", `{"password":"snow day"}`, "snow day"},
		{"key", "api_key=local-key", "local-key"},
		{"token", "access_token: access-value", "access-value"},
		{"flag", "--password=flag-value", "flag-value"},
		{"separate flag", "--password flag-space-value", "flag-space-value"},
		{"bearer", "Authorization: Bearer bearer-value", "bearer-value"},
		{"bearer punctuation", "(Bearer bearer-value) repeated bearer-value", "bearer-value"},
		{"url", "redis://:redis-pass@cache:6379/0", "redis-pass"},
		{"query", "https://db.internal/?password=query-pass&mode=read", "query-pass"},
		{"private key", "-----BEGIN RSA PRIVATE KEY-----\nsensitive material\n-----END RSA PRIVATE KEY-----", "sensitive material"},
		{"groq key", "gsk_12345678901234567890", "gsk_12345678901234567890"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := redact.Evidence(domain.Evidence{Logs: tc.text})
			if strings.Contains(e.Logs, tc.secret) {
				t.Fatalf("leaked credential: %s", e.Logs)
			}
			if !reflect.DeepEqual(e, redact.Evidence(e)) {
				t.Fatalf("redaction is not idempotent: %s -> %s", e.Logs, redact.Evidence(e).Logs)
			}
		})
	}
}

func TestExtraSecretsAndAllStrings(t *testing.T) {
	const secret = "extra-credential"
	e := domain.Evidence{Container: domain.Container{ID: secret, Name: secret, Image: secret, State: secret, Status: secret}, Error: secret, Health: secret, Logs: secret, Env: []string{secret}, Command: []string{secret}, Networks: []string{secret}, Warnings: []string{secret}, HealthLogs: []string{secret}}
	result, _ := json.Marshal(redact.Evidence(e, secret))
	if strings.Contains(string(result), secret) {
		t.Fatalf("unhandled evidence string: %s", result)
	}
}

func TestURLValuedSecretIsNotTreatedAsConnectionContext(t *testing.T) {
	const secret = "https://credential.example/opaque-secret"
	e := redact.Evidence(domain.Evidence{Env: []string{"PASSWORD=" + secret}, Logs: secret, Command: []string{secret}})
	data, _ := json.Marshal(e)
	if strings.Contains(string(data), secret) {
		t.Fatalf("URL-valued password leaked: %s", data)
	}
}

func TestCommandCredentialsAreDiscoveredAcrossArguments(t *testing.T) {
	for _, args := range [][]string{
		{"app", "--password", "plain-credential-123"},
		{"app", "--api-key", "https://credential.example/opaque-secret?x=1&y=2"},
		{"app", "--password=https://credential.example/opaque-secret?x=1&y=2"},
	} {
		secret := args[len(args)-1]
		if _, v, ok := strings.Cut(secret, "="); ok && strings.HasPrefix(secret, "--") {
			secret = v
		}
		safe := redact.Evidence(domain.Evidence{Command: args, Logs: "repeated " + secret, HealthLogs: []string{secret}})
		data, _ := json.Marshal(safe)
		if strings.Contains(string(data), secret) || strings.Contains(string(data), "opaque-secret") || strings.Contains(string(data), "y=2") {
			t.Fatalf("leaked command credential: %s", data)
		}
		if !reflect.DeepEqual(safe, redact.Evidence(safe)) {
			t.Fatalf("non-idempotent redaction: %s", data)
		}
	}
}
func TestURLValuedSensitiveAssignment(t *testing.T) {
	for _, value := range []string{"--password=https://credential.example/opaque-secret?x=1&y=2", "--password=https://credential.example/opaque-secret", `{"token":"https://credential.example/opaque-secret"}`} {
		safe := redact.Evidence(domain.Evidence{Logs: value})
		if strings.Contains(safe.Logs, "credential.example") || strings.Contains(safe.Logs, "opaque-secret") || strings.Contains(safe.Logs, "y=2") {
			t.Fatal(safe.Logs)
		}
	}
}
