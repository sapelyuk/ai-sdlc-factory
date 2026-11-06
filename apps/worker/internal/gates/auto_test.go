package gates

import (
	"context"
	"strings"
	"testing"
)

func TestAutoGateMissingCondition(t *testing.T) {
	g := AutoGate{Name: GateCIGreen}
	_, err := g.Evaluate(context.Background(), Facts{})
	if err == nil {
		t.Fatal("expected error for nil condition")
	}
	var mce MissingConditionError
	if !errorsAs(err, &mce) {
		t.Errorf("expected MissingConditionError, got %T", err)
	}
}

func TestCIGateConditions(t *testing.T) {
	tests := []struct {
		name     string
		facts    Facts
		wantPass bool
		contains string
	}{
		{
			name:     "all green",
			facts:    Facts{EvidenceCISuccess: true, EvidenceConventionalCommits: true, EvidenceCoverage: 0.9},
			wantPass: true,
		},
		{
			name:     "ci not green",
			facts:    Facts{EvidenceCISuccess: false, EvidenceConventionalCommits: true, EvidenceCoverage: 0.9},
			wantPass: false,
			contains: "not green",
		},
		{
			name:     "non conventional commits",
			facts:    Facts{EvidenceCISuccess: true, EvidenceConventionalCommits: false, EvidenceCoverage: 0.9},
			wantPass: false,
			contains: "Conventional Commits",
		},
		{
			name:     "coverage below default threshold",
			facts:    Facts{EvidenceCISuccess: true, EvidenceConventionalCommits: true, EvidenceCoverage: 0.5},
			wantPass: false,
			contains: "below the required",
		},
		{
			name: "custom threshold respected",
			facts: Facts{
				EvidenceCISuccess:           true,
				EvidenceConventionalCommits: true,
				EvidenceCoverage:            0.5,
				EvidenceCoverageThreshold:   0.4,
			},
			wantPass: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := AutoGate{Name: GateCIGreen, Condition: ciCondition}.Evaluate(context.Background(), tt.facts)
			if err != nil {
				t.Fatal(err)
			}
			if res.IsPassed() != tt.wantPass {
				t.Errorf("IsPassed()=%v, want %v (reason: %s)", res.IsPassed(), tt.wantPass, res.Reason)
			}
			if tt.contains != "" && !strings.Contains(res.Reason, tt.contains) {
				t.Errorf("reason %q does not contain %q", res.Reason, tt.contains)
			}
		})
	}
}

func TestQAGateConditions(t *testing.T) {
	tests := []struct {
		name     string
		facts    Facts
		wantPass bool
		contains string
	}{
		{
			name:     "all green",
			facts:    Facts{EvidenceTestsGreen: true, EvidenceCoverage: 0.9},
			wantPass: true,
		},
		{
			name:     "tests not green",
			facts:    Facts{EvidenceTestsGreen: false, EvidenceCoverage: 0.9},
			wantPass: false,
			contains: "not green",
		},
		{
			name: "critical bug open",
			facts: Facts{
				EvidenceTestsGreen:       true,
				EvidenceCoverage:         0.9,
				EvidenceOpenCriticalBugs: 1,
			},
			wantPass: false,
			contains: "critical defect",
		},
		{
			name: "high severity bug open",
			facts: Facts{
				EvidenceTestsGreen:   true,
				EvidenceCoverage:     0.9,
				EvidenceOpenHighBugs: 2,
			},
			wantPass: false,
			contains: "high-severity defect",
		},
		{
			name:     "coverage below threshold",
			facts:    Facts{EvidenceTestsGreen: true, EvidenceCoverage: 0.7},
			wantPass: false,
			contains: "below the required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := AutoGate{Name: GateQAPassed, Condition: qaCondition}.Evaluate(context.Background(), tt.facts)
			if err != nil {
				t.Fatal(err)
			}
			if res.IsPassed() != tt.wantPass {
				t.Errorf("IsPassed()=%v, want %v (reason: %s)", res.IsPassed(), tt.wantPass, res.Reason)
			}
			if tt.contains != "" && !strings.Contains(res.Reason, tt.contains) {
				t.Errorf("reason %q does not contain %q", res.Reason, tt.contains)
			}
		})
	}
}

func TestSBOMGate(t *testing.T) {
	tests := []struct {
		name     string
		facts    Facts
		wantPass bool
		contains string
	}{
		{
			name:     "both ok",
			facts:    Facts{EvidenceSBOMPresent: true, EvidenceImageSigned: true},
			wantPass: true,
		},
		{
			name:     "no sbom",
			facts:    Facts{EvidenceSBOMPresent: false, EvidenceImageSigned: true},
			wantPass: false,
			contains: "no SBOM",
		},
		{
			name:     "unsigned",
			facts:    Facts{EvidenceSBOMPresent: true, EvidenceImageSigned: false},
			wantPass: false,
			contains: "signature",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := AutoGate{Name: GateSBOMSigned, Condition: sbomCondition}.Evaluate(context.Background(), tt.facts)
			if err != nil {
				t.Fatal(err)
			}
			if res.IsPassed() != tt.wantPass {
				t.Errorf("IsPassed()=%v, want %v (reason: %s)", res.IsPassed(), tt.wantPass, res.Reason)
			}
			if tt.contains != "" && !strings.Contains(res.Reason, tt.contains) {
				t.Errorf("reason %q does not contain %q", res.Reason, tt.contains)
			}
		})
	}
}

func TestDeployGate(t *testing.T) {
	tests := []struct {
		name     string
		facts    Facts
		wantPass bool
		contains string
	}{
		{
			name:     "healthy",
			facts:    Facts{EvidenceHealthcheckOK: true, EvidenceMetricsNormal: true},
			wantPass: true,
		},
		{
			name:     "healthcheck failed",
			facts:    Facts{EvidenceHealthcheckOK: false, EvidenceMetricsNormal: true},
			wantPass: false,
			contains: "healthcheck",
		},
		{
			name:     "metrics abnormal",
			facts:    Facts{EvidenceHealthcheckOK: true, EvidenceMetricsNormal: false},
			wantPass: false,
			contains: "metrics",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := AutoGate{Name: GateDeployHealthy, Condition: deployCondition}.Evaluate(context.Background(), tt.facts)
			if err != nil {
				t.Fatal(err)
			}
			if res.IsPassed() != tt.wantPass {
				t.Errorf("IsPassed()=%v, want %v (reason: %s)", res.IsPassed(), tt.wantPass, res.Reason)
			}
			if tt.contains != "" && !strings.Contains(res.Reason, tt.contains) {
				t.Errorf("reason %q does not contain %q", res.Reason, tt.contains)
			}
		})
	}
}

func TestConformanceGate(t *testing.T) {
	tests := []struct {
		name     string
		drift    int
		wantPass bool
		contains string
	}{
		{"ok", DriftOK, true, ""},
		{"warning", DriftWarning, true, ""},
		{"critical", DriftCritical, false, "exceeds warning"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := AutoGate{Name: GateConformanceOK, Condition: conformanceCondition}.Evaluate(
				context.Background(), Facts{EvidenceDriftLevel: tt.drift})
			if err != nil {
				t.Fatal(err)
			}
			if res.IsPassed() != tt.wantPass {
				t.Errorf("IsPassed()=%v, want %v (reason: %s)", res.IsPassed(), tt.wantPass, res.Reason)
			}
			if tt.contains != "" && !strings.Contains(res.Reason, tt.contains) {
				t.Errorf("reason %q does not contain %q", res.Reason, tt.contains)
			}
		})
	}
}

func TestSemanticGate(t *testing.T) {
	res, err := AutoGate{Name: GateSemanticValid, Condition: semanticCondition}.Evaluate(
		context.Background(), Facts{EvidenceSemanticValid: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsPassed() {
		t.Errorf("expected pass, reason=%s", res.Reason)
	}

	res, err = AutoGate{Name: GateSemanticValid, Condition: semanticCondition}.Evaluate(
		context.Background(), Facts{EvidenceSemanticValid: false})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsPassed() {
		t.Error("expected block for invalid semantic")
	}
}

// errorsAs is a minimal wrapper to avoid importing errors in every test file.
func errorsAs(err error, target any) bool {
	for err != nil {
		switch e := err.(type) {
		case MissingConditionError:
			if p, ok := target.(*MissingConditionError); ok {
				*p = e
				return true
			}
		case UnknownGateError:
			if p, ok := target.(*UnknownGateError); ok {
				*p = e
				return true
			}
		case UnknownGateTypeError:
			if p, ok := target.(*UnknownGateTypeError); ok {
				*p = e
				return true
			}
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
