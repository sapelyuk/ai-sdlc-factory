package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsSensitiveKey(t *testing.T) {
	sensitive := []string{
		"token", "access_token", "X-Access-Token", "api_key", "APIKEY",
		"password", "client_secret", "credentials", "authorization",
		"private_key", "session_cookie", "DATABASE_DSN", "connection_string",
		"vault_token", "bearer",
	}
	for _, key := range sensitive {
		if !IsSensitiveKey(key) {
			t.Errorf("IsSensitiveKey(%q) = false, want true", key)
		}
	}

	plain := []string{"tool_name", "risk_level", "result", "model", "profile", "duration_ms", "run_id"}
	for _, key := range plain {
		if IsSensitiveKey(key) {
			t.Errorf("IsSensitiveKey(%q) = true, want false", key)
		}
	}
}

func TestRedactReplacesSecretKeys(t *testing.T) {
	out := Redact(map[string]any{
		"tool_name":    "git.push",
		"access_token": "shhh",
		"nested": map[string]any{
			"api_key":  "shhh",
			"endpoint": "https://example.com",
		},
		"list": []any{
			map[string]any{"password": "shhh"},
			"plain",
		},
	})

	if out["tool_name"] != "git.push" {
		t.Fatalf("non-sensitive key was modified: %v", out["tool_name"])
	}
	if out["access_token"] != RedactedValue {
		t.Fatalf("access_token = %v, want %s", out["access_token"], RedactedValue)
	}
	nested := out["nested"].(map[string]any)
	if nested["api_key"] != RedactedValue {
		t.Fatalf("nested api_key = %v", nested["api_key"])
	}
	if nested["endpoint"] != "https://example.com" {
		t.Fatalf("nested endpoint was modified: %v", nested["endpoint"])
	}
	first := out["list"].([]any)[0].(map[string]any)
	if first["password"] != RedactedValue {
		t.Fatalf("list item password = %v", first["password"])
	}
}

func TestRedactScansStringValues(t *testing.T) {
	cases := map[string]string{
		"github token":  "failed with ghp_integrationsecret0123456789 in headers",
		"aws key":       "access key AKIAIOSFODNN7EXAMPLE rejected",
		"slack token":   "failed with " + "xoxb" + "-" + "test1234567890",
		"jwt":           "authorization rejected: eyJhbGciOiJIUzI1NiIs.eyJzdWIiOiIxMjM0NTY3ODkw.c2lnbmF0dXJlYWJj",
		"pem":           "loaded -----BEGIN RSA PRIVATE KEY----- blob",
		"dsn":           "cannot reach postgres://user:supersecret@db:5432/app",
		"inline secret": "config password=hunter2 not accepted",
	}
	for name, in := range cases {
		got := RedactString(in)
		if !strings.Contains(got, RedactedValue) {
			t.Errorf("%s: secret not redacted: %q", name, got)
		}
		for _, leak := range []string{"hunter2", "supersecret", "AKIAIOSFODNN7EXAMPLE", "ghp_integrationsecret0123456789", "BEGIN RSA PRIVATE KEY"} {
			if strings.Contains(got, leak) {
				t.Errorf("%s: leaked %q in %q", name, leak, got)
			}
		}
	}
}

func TestRedactLeavesSafeStrings(t *testing.T) {
	in := "tool git.push denied by policy R3"
	if got := RedactString(in); got != in {
		t.Fatalf("safe string was rewritten: %q", got)
	}
}

func TestRedactDoesNotMutateInput(t *testing.T) {
	original := map[string]any{"token": "raw"}
	Redact(original)
	if original["token"] != "raw" {
		t.Fatal("Redact must not mutate the caller's map")
	}
	if Redact(nil) != nil {
		t.Fatal("Redact(nil) must stay nil")
	}
}

func TestRedactBoundsRecursion(t *testing.T) {
	deep := map[string]any{}
	cursor := deep
	for i := 0; i < maxRedactDepth+10; i++ {
		next := map[string]any{"level": i}
		cursor["child"] = next
		cursor = next
	}
	out := Redact(deep)
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("deeply nested details must stay encodable: %v", err)
	}
	if !strings.Contains(string(encoded), RedactedValue) {
		t.Fatalf("expected the depth limit to cut the payload short: %s", encoded)
	}
}

// TestRedactRunsBeforeHashing is the guarantee required by issue #14 rule 4:
// the hashed and stored payload never contains the secret.
func TestRedactRunsBeforeHashing(t *testing.T) {
	logger, store := fixedClock(t)
	_, err := logger.Append(t.Context(), Event{
		WorkspaceID: testWorkspace,
		ActorID:     SystemActor("tool-gateway"),
		Action:      ActionToolCall,
		ResourceID:  "git.push",
		Details: map[string]any{
			"tool_name": "git.push",
			"token":     "ghp_integrationsecret0123456789",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	stored := store.Records(testWorkspace)[0]
	if strings.Contains(string(stored.Details), "ghp_integrationsecret0123456789") {
		t.Fatalf("secret leaked into details JSON: %s", stored.Details)
	}
	if !strings.Contains(string(stored.Details), RedactedValue) {
		t.Fatalf("expected %s in details, got %s", RedactedValue, stored.Details)
	}
	if err := VerifyChain(store.Records(testWorkspace)); err != nil {
		t.Fatalf("redaction must not break the chain: %v", err)
	}
}
