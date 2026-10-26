package credentials

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newVaultServer(t *testing.T, body string, status int) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if !strings.HasPrefix(r.URL.Path, "/v1/secret/data/") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("X-Vault-Token") != "test-token" {
			t.Errorf("missing Vault token header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestVaultBrokerKVv2(t *testing.T) {
	srv, hits := newVaultServer(t, `{"data":{"data":{"token":"ghp_supersecret"},"metadata":{"version":1}}}`, http.StatusOK)
	broker, err := NewVaultBroker(srv.URL, "test-token", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	data, err := broker.Fetch(context.Background(), "secret/data/github")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if data["token"] != "ghp_supersecret" {
		t.Fatalf("token = %q", data["token"])
	}

	// The second read must be served from the cache.
	if _, err := broker.Fetch(context.Background(), "secret/data/github"); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("Vault hits = %d, want 1", got)
	}
}

func TestVaultBrokerKVv1(t *testing.T) {
	srv, _ := newVaultServer(t, `{"data":{"token":"ghp_v1secret"}}`, http.StatusOK)
	broker, err := NewVaultBroker(srv.URL, "test-token", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	data, err := broker.Fetch(context.Background(), "secret/data/github")
	if err != nil {
		t.Fatal(err)
	}
	if data["token"] != "ghp_v1secret" {
		t.Fatalf("token = %q", data["token"])
	}
}

func TestVaultBrokerNotFound(t *testing.T) {
	srv, _ := newVaultServer(t, `{"errors":[]}`, http.StatusNotFound)
	broker, err := NewVaultBroker(srv.URL, "test-token", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, err = broker.Fetch(context.Background(), "secret/data/github")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestVaultBrokerRejectsEmptyInput(t *testing.T) {
	if _, err := NewVaultBroker("", "token", time.Minute); err == nil {
		t.Fatal("expected an error for an empty address")
	}
	if _, err := NewVaultBroker("http://localhost:8200", "", time.Minute); err == nil {
		t.Fatal("expected an error for an empty token")
	}

	broker, err := NewVaultBroker("http://localhost:8200", "token", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Fetch(context.Background(), ""); err == nil {
		t.Fatal("expected an error for an empty path")
	}
}

func TestRequirementsFor(t *testing.T) {
	reqs := RequirementsFor("git.push")
	if len(reqs) != 1 {
		t.Fatalf("requirements = %d, want 1", len(reqs))
	}
	if reqs[0].Path != "secret/data/github" || reqs[0].Key != "token" || reqs[0].Env != "GITHUB_TOKEN" {
		t.Fatalf("unexpected requirement: %+v", reqs[0])
	}

	// Callers must not be able to mutate the global table.
	reqs[0].Path = "secret/data/other"
	if got := RequirementsFor("git.push"); got[0].Path != "secret/data/github" {
		t.Fatalf("global requirements were mutated: %+v", got[0])
	}

	if len(RequirementsFor("fs.read")) != 0 {
		t.Fatal("fs.read must not require credentials")
	}
}

func TestRedactor(t *testing.T) {
	r := NewRedactor()
	r.Add("ghp_supersecret", "")

	text := "token=ghp_supersecret other=github_pat_abcdefghijklmnopqrstuvwxyz123456"
	got := r.Redact(text)
	if strings.Contains(got, "ghp_supersecret") {
		t.Fatalf("known secret leaked: %s", got)
	}
	if strings.Contains(got, "github_pat_") {
		t.Fatalf("token-shaped value leaked: %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected a redaction marker: %s", got)
	}
}

func TestRedactorOverlappingSecrets(t *testing.T) {
	r := NewRedactor()
	r.Add("secret", "secret-longer")
	if got := r.Redact("secret-longer"); got != "[REDACTED]" {
		t.Fatalf("got %q", got)
	}
}

func TestErrSecretNotFoundIsWrapped(t *testing.T) {
	srv, _ := newVaultServer(t, `{"data":{"data":{}}}`, http.StatusOK)
	broker, err := NewVaultBroker(srv.URL, "test-token", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, err = broker.Fetch(context.Background(), "secret/data/github")
	if !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("error = %v, want ErrSecretNotFound", err)
	}
}
