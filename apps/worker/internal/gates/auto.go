package gates

import (
	"context"
	"fmt"
)

// Evidence fact keys read by the gate conditions. Producers (activities,
// routers, the API) set these keys; a missing key means the evidence is absent
// and the gate does not pass.
const (
	// EvidenceCISuccess is a bool: every CI check of the PR is green.
	EvidenceCISuccess = "ci.success"
	// EvidenceCoverage is a float64 in [0,1]: new code coverage.
	EvidenceCoverage = "ci.coverage"
	// EvidenceConventionalCommits is a bool: commit messages follow the
	// Conventional Commits spec.
	EvidenceConventionalCommits = "ci.conventional_commits"

	// EvidenceTestsGreen is a bool: every QA test level passed.
	EvidenceTestsGreen = "qa.tests_green"
	// EvidenceCoverageThreshold is a float64 in [0,1]: the required coverage.
	EvidenceCoverageThreshold = "qa.coverage_threshold"
	// EvidenceOpenCriticalBugs is an int: unresolved critical defects.
	EvidenceOpenCriticalBugs = "qa.open_critical_bugs"
	// EvidenceOpenHighBugs is an int: unresolved high-severity defects.
	EvidenceOpenHighBugs = "qa.open_high_bugs"

	// EvidenceSBOMPresent is a bool: a SBOM was produced for the image.
	EvidenceSBOMPresent = "sbom.present"
	// EvidenceImageSigned is a bool: the image signature verified.
	EvidenceImageSigned = "sbom.image_signed"

	// EvidenceHealthcheckOK is a bool: the deployed health endpoint is green.
	EvidenceHealthcheckOK = "deploy.healthcheck_ok"
	// EvidenceMetricsNormal is a bool: no SLO-breaking metric anomalies.
	EvidenceMetricsNormal = "deploy.metrics_normal"

	// EvidenceDriftLevel is an int: 0 ok, 1 warning, 2 critical
	// (semantic-indexer drift).
	EvidenceDriftLevel = "conformance.drift_level"

	// EvidenceSemanticValid is a bool: the ontology inference rules pass.
	EvidenceSemanticValid = "semantic.valid"

	// EvidenceReviewApproved is a bool: the reviewer approved the package.
	EvidenceReviewApproved = "review.approved"
	// EvidenceReviewAuthor is a string: the agent that produced the package.
	EvidenceReviewAuthor = "review.author"
	// EvidenceReviewReviewer is a string: the agent that reviewed it.
	EvidenceReviewReviewer = "review.reviewer"
	// EvidenceReviewRound is an int: the current review round number.
	EvidenceReviewRound = "review.round"

	// EvidenceApprovalID is a string: the approval request to read the human
	// decision from (human_approval gates).
	EvidenceApprovalID = "approval.id"
)

// DefaultCoverageThreshold is the minimum new code coverage required by the
// contribution rules (AGENTS.md code rule 2: new code coverage >= 80%).
const DefaultCoverageThreshold = 0.80

// Condition is the deterministic predicate of an auto gate. It returns whether
// the transition is allowed and, when it is not, why.
type Condition func(facts Facts) (bool, string)

// AutoGate evaluates an auto gate: a pure condition over the evidence facts.
type AutoGate struct {
	Name      string
	Condition Condition
}

// Evaluate implements Evaluator.
func (g AutoGate) Evaluate(_ context.Context, facts Facts) (Result, error) {
	if g.Condition == nil {
		return Result{}, MissingConditionError{Name: g.Name}
	}
	pass, reason := g.Condition(facts)
	if pass {
		return Result{Approved: true}, nil
	}
	if reason == "" {
		reason = fmt.Sprintf("gate %q condition is not satisfied", g.Name)
	}
	return Result{Reason: reason}, nil
}

// ciCondition checks CI green, coverage and commit conventions
// (SDLC_STATE_MACHINE.md section 3.9).
func ciCondition(facts Facts) (bool, string) {
	if !facts.GetBool(EvidenceCISuccess) {
		return false, "CI checks are not green"
	}
	if !facts.GetBool(EvidenceConventionalCommits) {
		return false, "commit messages do not follow Conventional Commits"
	}
	// The producer may pin a project-specific threshold; the platform default
	// is 80% (AGENTS.md code rule 2).
	threshold := DefaultCoverageThreshold
	if v := facts.GetFloat64(EvidenceCoverageThreshold); v > 0 {
		threshold = v
	}
	if coverage := facts.GetFloat64(EvidenceCoverage); coverage < threshold {
		return false, fmt.Sprintf("new code coverage %.1f%% is below the required %.1f%%",
			coverage*100, threshold*100)
	}
	return true, ""
}

// qaCondition checks tests, coverage and defect closure
// (SDLC_STATE_MACHINE.md section 3.12).
func qaCondition(facts Facts) (bool, string) {
	if !facts.GetBool(EvidenceTestsGreen) {
		return false, "QA test levels are not green"
	}
	if critical := facts.GetInt(EvidenceOpenCriticalBugs); critical > 0 {
		return false, fmt.Sprintf("%d critical defect(s) still open", critical)
	}
	if high := facts.GetInt(EvidenceOpenHighBugs); high > 0 {
		return false, fmt.Sprintf("%d high-severity defect(s) still open", high)
	}
	threshold := DefaultCoverageThreshold
	if v := facts.GetFloat64(EvidenceCoverageThreshold); v > 0 {
		threshold = v
	}
	if coverage := facts.GetFloat64(EvidenceCoverage); coverage < threshold {
		return false, fmt.Sprintf("coverage %.1f%% is below the required %.1f%%",
			coverage*100, threshold*100)
	}
	return true, ""
}

// sbomCondition checks the SBOM exists and the image signature verifies.
func sbomCondition(facts Facts) (bool, string) {
	if !facts.GetBool(EvidenceSBOMPresent) {
		return false, "no SBOM was produced for the image"
	}
	if !facts.GetBool(EvidenceImageSigned) {
		return false, "image signature verification failed"
	}
	return true, ""
}

// deployCondition checks the health endpoint and metric sanity.
func deployCondition(facts Facts) (bool, string) {
	if !facts.GetBool(EvidenceHealthcheckOK) {
		return false, "deployment healthcheck failed"
	}
	if !facts.GetBool(EvidenceMetricsNormal) {
		return false, "post-deploy metrics are outside the normal range"
	}
	return true, ""
}

// conformanceCondition accepts drift up to warning level; critical drift
// blocks the merge (SDLC_STATE_MACHINE.md section 5, item 5).
func conformanceCondition(facts Facts) (bool, string) {
	if level := facts.GetInt(EvidenceDriftLevel); level > DriftWarning {
		return false, fmt.Sprintf("architecture drift level %d exceeds warning", level)
	}
	return true, ""
}

// semanticCondition checks the ontology inference rules over the SLPr/AM
// change (ONTOLOGY_SPEC.md section 7).
func semanticCondition(facts Facts) (bool, string) {
	if !facts.GetBool(EvidenceSemanticValid) {
		return false, "semantic inference rules are violated"
	}
	return true, ""
}

// Drift levels of the conformance report. The ordering is part of the gate
// contract: a value above DriftWarning blocks the transition.
const (
	DriftOK       = 0
	DriftWarning  = 1
	DriftCritical = 2
)

// AutoConditions returns the condition of every auto gate of
// SDLC_STATE_MACHINE.md section 4.
func AutoConditions() map[string]Condition {
	return map[string]Condition{
		GateCIGreen:       ciCondition,
		GateQAPassed:      qaCondition,
		GateSBOMSigned:    sbomCondition,
		GateDeployHealthy: deployCondition,
		GateConformanceOK: conformanceCondition,
		GateSemanticValid: semanticCondition,
	}
}
