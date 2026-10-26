package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/open-policy-agent/opa/v1/rego"
)

// Embedded evaluates the Rego policies in-process.
//
// It is used when OPA_URL is empty (tests, local runs without OPA) and is
// deliberately the same policy source that OPA loads in Docker Compose.
type Embedded struct {
	query rego.PreparedEvalQuery
}

// NewEmbedded compiles every *.rego file in dir.
func NewEmbedded(dir string) (*Embedded, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.rego"))
	if err != nil {
		return nil, fmt.Errorf("glob policy files: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no Rego files found in %s", dir)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("policy file %s: %w", p, err)
		}
	}

	r := rego.New(
		rego.Query("data.ai_sdlc_factory.autonomy.decision"),
		rego.Load(paths, nil),
	)
	query, err := r.PrepareForEval(context.Background())
	if err != nil {
		return nil, fmt.Errorf("compile Rego policies: %w", err)
	}
	return &Embedded{query: query}, nil
}

// Authorize evaluates the policy locally.
func (e *Embedded) Authorize(ctx context.Context, req Request) (Decision, error) {
	rs, err := e.query.Eval(ctx, rego.EvalInput(req))
	if err != nil {
		return Decision{}, fmt.Errorf("%w: evaluate Rego: %v", ErrUnavailable, err)
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		return Decision{
			Allow:     false,
			RiskLevel: req.ToolRiskLevel,
			Reason:    "policy returned no decision",
		}, nil
	}

	raw, err := json.Marshal(rs[0].Expressions[0].Value)
	if err != nil {
		return Decision{}, fmt.Errorf("marshal Rego result: %w", err)
	}
	var d Decision
	if err := json.Unmarshal(raw, &d); err != nil {
		return Decision{}, fmt.Errorf("decode Rego result: %w", err)
	}
	if !validDecision(d) {
		return Decision{}, errors.New("policy returned a malformed decision")
	}
	return d, nil
}

func validDecision(d Decision) bool {
	switch d.RiskLevel {
	case "R0", "R1", "R2", "R3":
		return true
	default:
		return false
	}
}

// Ready always succeeds for the in-process engine.
func (e *Embedded) Ready(context.Context) error { return nil }
