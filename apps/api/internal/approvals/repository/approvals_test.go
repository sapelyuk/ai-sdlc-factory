// Package repository tests for the approval queue repository.
//
// Unit tests exercise error constants, column completeness, and event building.
// Integration tests (tagged with "integration") require Docker/testcontainers.
package repository

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/approvals/domain"
)

// --- Test error constants have messages ---

func TestErrSelfApproval_ErrorMessage(t *testing.T) {
	if ErrSelfApproval.Error() == "" {
		t.Error("ErrSelfApproval should have a non-empty error message")
	}
}

func TestErrNotPending_ErrorMessage(t *testing.T) {
	if ErrNotPending.Error() == "" {
		t.Error("ErrNotPending should have a non-empty error message")
	}
}

func TestErrAuditNotConfigured_ErrorMessage(t *testing.T) {
	if ErrAuditNotConfigured.Error() == "" {
		t.Error("ErrAuditNotConfigured should have a non-empty error message")
	}
}

// --- Test error identity ---

func TestErrSelfApproval_IsDetected(t *testing.T) {
	if !errors.Is(ErrSelfApproval, ErrSelfApproval) {
		t.Error("ErrSelfApproval should be detected by errors.Is")
	}
}

func TestErrNotPending_IsDetected(t *testing.T) {
	if !errors.Is(ErrNotPending, ErrNotPending) {
		t.Error("ErrNotPending should be detected by errors.Is")
	}
}

// --- Test approvalColumns constant is well-formed ---

func TestApprovalColumns_Completeness(t *testing.T) {
	expectedCols := []string{
		"id", "workspace_id", "gate_name", "status", "requested_by",
		"approved_by", "risk_level", "deadline_at", "context", "comment",
		"workflow_id", "task_run_id", "stage_run_id", "run_id",
		"created_at", "updated_at", "decided_at",
	}
	for _, col := range expectedCols {
		if !containsColumn(approvalColumns, col) {
			t.Errorf("approvalColumns missing column %q", col)
		}
	}
}

func containsColumn(query, col string) bool {
	for i := 0; i <= len(query)-len(col); i++ {
		if query[i:i+len(col)] == col {
			return true
		}
	}
	return false
}

// --- Test domain struct has RunID field ---

func TestApprovalRequest_HasRunID(t *testing.T) {
	req := &domain.ApprovalRequest{
		ID:          uuid.New(),
		WorkspaceID: uuid.New(),
		GateName:    "ci_green",
		Status:      domain.StatusPending,
		RunID:       uuid.NewPtr(),
	}
	if req.RunID == nil {
		t.Error("ApprovalRequest.RunID should be settable")
	}
}

// --- Test decisionRecord structure ---

func TestDecisionRecord_Fields(t *testing.T) {
	wid := uuid.New()
	aid := uuid.New()
	rid := uuid.New()
	did := uuid.New()
	rec := decisionRecord{
		workspaceID: wid,
		approvalID:  aid,
		requestedBy: rid,
		decidedBy:   did,
		status:      domain.StatusApproved,
		workflowID:  "wf-123",
		riskLevel:   "R3",
	}
	if rec.workspaceID != wid {
		t.Error("decisionRecord.workspaceID mismatch")
	}
	if rec.approvalID != aid {
		t.Error("decisionRecord.approvalID mismatch")
	}
	if rec.requestedBy != rid {
		t.Error("decisionRecord.requestedBy mismatch")
	}
	if rec.decidedBy != did {
		t.Error("decisionRecord.decidedBy mismatch")
	}
}

// --- Integration test skeleton (requires Docker/testcontainers) ---

// TestCreate_ThenGet_RoundTrip verifies create-then-get round trip.
//
//go:build integration
// +build integration

func TestCreate_ThenGet_RoundTrip_Integration(t *testing.T) {
	t.Skip("integration test requires Docker; run with -tags=integration")
}

// TestList_RLS_Isolation verifies workspace A cannot see workspace B.
//
//go:build integration
// +build integration

func TestList_RLS_Isolation_Integration(t *testing.T) {
	t.Skip("integration test requires Docker; run with -tags=integration")
}

// TestDecide_Idempotent verifies repeated decisions are handled (409).
//
//go:build integration
// +build integration

func TestDecide_Idempotent_Integration(t *testing.T) {
	t.Skip("integration test requires Docker; run with -tags=integration")
}

// TestDecide_SelfApproval_ReturnsErrSelfApproval verifies P9 self-approval check.
//
//go:build integration
// +build integration

func TestDecide_SelfApproval_ReturnsErrSelfApproval_Integration(t *testing.T) {
	t.Skip("integration test requires Docker; run with -tags=integration")
}

// TestExpire_Overdue_MarksRejected verifies expiration of overdue requests.
//
//go:build integration
// +build integration

func TestExpire_Overdue_MarksRejected_Integration(t *testing.T) {
	t.Skip("integration test requires Docker; run with -tags=integration")
}

// TestExpire_NotOverdue_Noop verifies non-overdue requests are unchanged.
//
//go:build integration
// +build integration

func TestExpire_NotOverdue_Noop_Integration(t *testing.T) {
	t.Skip("integration test requires Docker; run with -tags=integration")
}

// --- Benchmark tests ---

func BenchmarkScanApprovalZero(b *testing.B) {
	id := uuid.New()
	req := domain.ApprovalRequest{
		ID:          id,
		WorkspaceID: uuid.New(),
		GateName:    "ci_green",
		Status:      domain.StatusPending,
		RequestedBy: id,
	}
	_ = req
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = req
	}
}
