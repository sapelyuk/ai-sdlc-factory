package identity

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func validHeaders() http.Header {
	h := http.Header{}
	h.Set(HeaderWorkspaceID, "11111111-1111-1111-1111-111111111111")
	h.Set(HeaderAgentID, "22222222-2222-2222-2222-222222222222")
	h.Set(HeaderAgentRole, "developer")
	h.Set(HeaderAutonomyLevel, "R1")
	return h
}

func TestFromHeaders(t *testing.T) {
	id, err := FromHeaders(validHeaders())
	if err != nil {
		t.Fatalf("FromHeaders: %v", err)
	}
	if id.Role != "developer" || id.AutonomyLevel != "R1" {
		t.Fatalf("unexpected identity: %+v", id)
	}
	if id.AuthorID != id.AgentID {
		t.Fatal("author must default to the agent ID")
	}
	if id.ApproverID != uuid.Nil {
		t.Fatal("approver must be empty by default")
	}
}

func TestFromHeadersDefaultsAutonomyToR0(t *testing.T) {
	h := validHeaders()
	h.Del(HeaderAutonomyLevel)
	id, err := FromHeaders(h)
	if err != nil {
		t.Fatal(err)
	}
	if id.AutonomyLevel != "R0" {
		t.Fatalf("autonomy = %q, want R0", id.AutonomyLevel)
	}
}

func TestFromHeadersApproverAndAuthor(t *testing.T) {
	h := validHeaders()
	h.Set(HeaderAuthorID, "33333333-3333-3333-3333-333333333333")
	h.Set(HeaderApproverID, "44444444-4444-4444-4444-444444444444")

	id, err := FromHeaders(h)
	if err != nil {
		t.Fatal(err)
	}
	if id.AuthorID.String() != "33333333-3333-3333-3333-333333333333" {
		t.Fatalf("author = %s", id.AuthorID)
	}
	if id.ApproverID.String() != "44444444-4444-4444-4444-444444444444" {
		t.Fatalf("approver = %s", id.ApproverID)
	}
}

func TestFromHeadersValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(http.Header)
		want   string
	}{
		{"missing workspace", func(h http.Header) { h.Del(HeaderWorkspaceID) }, HeaderWorkspaceID},
		{"missing agent", func(h http.Header) { h.Del(HeaderAgentID) }, HeaderAgentID},
		{"missing role", func(h http.Header) { h.Del(HeaderAgentRole) }, HeaderAgentRole},
		{"bad workspace uuid", func(h http.Header) { h.Set(HeaderWorkspaceID, "nope") }, "valid UUID"},
		{"nil workspace", func(h http.Header) {
			h.Set(HeaderWorkspaceID, uuid.Nil.String())
		}, "nil UUID"},
		{"bad author", func(h http.Header) { h.Set(HeaderAuthorID, "nope") }, HeaderAuthorID},
		{"bad approver", func(h http.Header) { h.Set(HeaderApproverID, "nope") }, HeaderApproverID},
		{"bad autonomy", func(h http.Header) { h.Set(HeaderAutonomyLevel, "R9") }, HeaderAutonomyLevel},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := validHeaders()
			tc.mutate(h)
			_, err := FromHeaders(h)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestContextRoundTrip(t *testing.T) {
	if _, err := From(context.Background()); err == nil {
		t.Fatal("expected an error for an empty context")
	}

	want := Identity{Role: "qa", AutonomyLevel: "R1"}
	got, err := From(With(context.Background(), want))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestValidRiskLevel(t *testing.T) {
	for _, level := range []string{"R0", "R1", "R2", "R3"} {
		if !ValidRiskLevel(level) {
			t.Errorf("%s must be valid", level)
		}
	}
	for _, level := range []string{"", "r1", "R4"} {
		if ValidRiskLevel(level) {
			t.Errorf("%s must be invalid", level)
		}
	}
}
