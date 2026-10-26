package policy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const policyDir = "../../../../packages/policy"

func newEmbedded(t *testing.T) *Embedded {
	t.Helper()
	e, err := NewEmbedded(policyDir)
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	return e
}

func TestEmbeddedDecisions(t *testing.T) {
	e := newEmbedded(t)

	tests := []struct {
		name       string
		req        Request
		wantAllow  bool
		wantRisk   string
		wantReason string
	}{
		{
			name: "developer may push",
			req: Request{
				Role: "developer", Tool: "git.push", ToolRiskLevel: "R1",
				AutonomyLevel: "R1", AuthorID: "author",
			},
			wantAllow: true,
			wantRisk:  "R1",
		},
		{
			name: "developer may not deploy to prod",
			req: Request{
				Role: "developer", Tool: "deploy.prod", ToolRiskLevel: "R3",
				AutonomyLevel: "R3", AuthorID: "author",
			},
			wantAllow:  false,
			wantRisk:   "R3",
			wantReason: "role is not allowed",
		},
		{
			name: "admin may push",
			req: Request{
				Role: "admin", Tool: "git.push", ToolRiskLevel: "R1",
				AutonomyLevel: "R1", AuthorID: "author",
			},
			wantAllow: true,
			wantRisk:  "R1",
		},
		{
			name: "R3 without approver is denied",
			req: Request{
				Role: "admin", Tool: "deploy.prod", ToolRiskLevel: "R3",
				AutonomyLevel: "R3", AuthorID: "author",
			},
			wantAllow:  false,
			wantRisk:   "R3",
			wantReason: "requires approval",
		},
		{
			name: "R3 with a different approver is allowed",
			req: Request{
				Role: "admin", Tool: "deploy.prod", ToolRiskLevel: "R3",
				AutonomyLevel: "R0", AuthorID: "author", ApproverID: "human",
			},
			wantAllow: true,
			wantRisk:  "R3",
		},
		{
			name: "R3 self-approval is denied",
			req: Request{
				Role: "admin", Tool: "deploy.prod", ToolRiskLevel: "R3",
				AutonomyLevel: "R3", AuthorID: "same", ApproverID: "same",
			},
			wantAllow:  false,
			wantRisk:   "R3",
			wantReason: "requires approval",
		},
		{
			name: "viewer may not push",
			req: Request{
				Role: "viewer", Tool: "git.push", ToolRiskLevel: "R1",
				AutonomyLevel: "R3", AuthorID: "author",
			},
			wantAllow:  false,
			wantRisk:   "R1",
			wantReason: "role is not allowed",
		},
		{
			name: "qa may run tests",
			req: Request{
				Role: "qa", Tool: "test.run", ToolRiskLevel: "R1",
				AutonomyLevel: "R1", AuthorID: "author",
			},
			wantAllow: true,
			wantRisk:  "R1",
		},
		{
			name: "R2 exceeds R1 autonomy",
			req: Request{
				Role: "devops", Tool: "deploy.staging", ToolRiskLevel: "R2",
				AutonomyLevel: "R1", AuthorID: "author",
			},
			wantAllow:  false,
			wantRisk:   "R2",
			wantReason: "exceeds the autonomy profile",
		},
		{
			name: "R2 is allowed at R2 autonomy",
			req: Request{
				Role: "devops", Tool: "deploy.staging", ToolRiskLevel: "R2",
				AutonomyLevel: "R2", AuthorID: "author",
			},
			wantAllow: true,
			wantRisk:  "R2",
		},
		{
			name: "unknown role is denied",
			req: Request{
				Role: "contractor", Tool: "git.diff", ToolRiskLevel: "R0",
				AutonomyLevel: "R3", AuthorID: "author",
			},
			wantAllow:  false,
			wantRisk:   "R0",
			wantReason: "role is not allowed",
		},
		{
			name: "schema cannot downgrade a dangerous tool",
			req: Request{
				Role: "admin", Tool: "deploy.prod", ToolRiskLevel: "R0",
				AutonomyLevel: "R3", AuthorID: "author",
			},
			wantAllow:  false,
			wantRisk:   "R3",
			wantReason: "requires approval",
		},
		{
			name: "explicit deny wins over admin",
			req: Request{
				Role: "admin", Tool: "secrets.read", ToolRiskLevel: "R0",
				AutonomyLevel: "R3", AuthorID: "author", RoleDeny: []string{"secrets.read"},
			},
			wantAllow:  false,
			wantRisk:   "R3",
			wantReason: "explicitly denied",
		},
		{
			name: "catalog allow-list restricts the static matrix",
			req: Request{
				Role: "developer", Tool: "git.push", ToolRiskLevel: "R1",
				AutonomyLevel: "R1", AuthorID: "author", RoleTools: []string{"fs.read"},
			},
			wantAllow:  false,
			wantRisk:   "R1",
			wantReason: "role is not allowed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := e.Authorize(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("Authorize: %v", err)
			}
			if d.Allow != tc.wantAllow {
				t.Fatalf("allow = %v, want %v (reason %q)", d.Allow, tc.wantAllow, d.Reason)
			}
			if d.RiskLevel != tc.wantRisk {
				t.Fatalf("risk = %q, want %q", d.RiskLevel, tc.wantRisk)
			}
			if tc.wantReason != "" && !strings.Contains(d.Reason, tc.wantReason) {
				t.Fatalf("reason = %q, want it to contain %q", d.Reason, tc.wantReason)
			}
		})
	}
}

func TestEmbeddedRequiresPolicyFiles(t *testing.T) {
	if _, err := NewEmbedded(t.TempDir()); err == nil {
		t.Fatal("expected an error for an empty policy directory")
	}
}

func TestOPAClient(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		gotPath = r.URL.Path
		var payload struct {
			Input Request `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode input: %v", err)
		}
		if payload.Input.Tool != "git.push" {
			t.Errorf("tool = %q", payload.Input.Tool)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": Decision{Allow: true, RiskLevel: "R1", Reason: "allowed"},
		})
	}))
	defer srv.Close()

	engine, err := NewOPA(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	d, err := engine.Authorize(context.Background(), Request{Tool: "git.push", Role: "developer"})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if !d.Allow || d.RiskLevel != "R1" {
		t.Fatalf("unexpected decision: %+v", d)
	}
	if gotPath != "/"+DecisionPath {
		t.Fatalf("path = %q", gotPath)
	}
	if err := engine.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
}

func TestOPAClientFailClosed(t *testing.T) {
	t.Run("no result", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		}))
		defer srv.Close()

		engine, err := NewOPA(srv.URL, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		d, err := engine.Authorize(context.Background(), Request{Tool: "git.push", ToolRiskLevel: "R1"})
		if err != nil {
			t.Fatal(err)
		}
		if d.Allow {
			t.Fatal("a missing result must deny the call")
		}
		if d.Reason == "" {
			t.Fatal("expected a reason")
		}
	})

	t.Run("server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srv.Close()

		engine, err := NewOPA(srv.URL, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_, err = engine.Authorize(context.Background(), Request{Tool: "git.push"})
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("error = %v, want ErrUnavailable", err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`not json`))
		}))
		defer srv.Close()

		engine, err := NewOPA(srv.URL, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_, err = engine.Authorize(context.Background(), Request{Tool: "git.push"})
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("error = %v, want ErrUnavailable", err)
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		engine, err := NewOPA("http://127.0.0.1:1", 200*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		_, err = engine.Authorize(context.Background(), Request{Tool: "git.push"})
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("error = %v, want ErrUnavailable", err)
		}
	})

	t.Run("empty url", func(t *testing.T) {
		if _, err := NewOPA("", time.Second); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestOPAReadyFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	engine, err := NewOPA(srv.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Ready(context.Background()); err == nil {
		t.Fatal("expected a readiness error")
	}
}

func TestDecisionContext(t *testing.T) {
	ctx := context.Background()
	d := Decision{Allow: true, RiskLevel: "R1"}
	ctx = WithDecision(ctx, d)
	got, ok := DecisionFrom(ctx)
	if !ok || got != d {
		t.Fatalf("DecisionFrom = %+v, %v", got, ok)
	}

	ctx = WithDecisions(context.Background(), map[string]Decision{"git.push": d})
	batch, ok := DecisionsFrom(ctx)
	if !ok || batch["git.push"] != d {
		t.Fatalf("DecisionsFrom = %+v, %v", batch, ok)
	}
}
