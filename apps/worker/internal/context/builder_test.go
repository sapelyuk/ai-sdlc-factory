package context

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
)

func TestBuilder_Build_WithBothServices(t *testing.T) {
	semSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nodes := []client.SemNode{
			{ID: "n1", Kind: "FR", Name: "FR-OPS", Layer: "SLP"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(nodes)
	}))
	defer semSrv.Close()

	kbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		frags := []client.KBFragment{
			{DocID: "d1", Snippet: "Budget must stop at 80%.", Score: 0.95},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(frags)
	}))
	defer kbSrv.Close()

	b := NewBuilder(
		client.NewSemanticClient(semSrv.URL),
		client.NewKnowledgeClient(kbSrv.URL),
	)

	result, err := b.Build(context.Background(), "project-1", "budget guard")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "Semantic Context") {
		t.Error("expected semantic section")
	}
	if !strings.Contains(result.Content, "Knowledge") {
		t.Error("expected knowledge section")
	}
	if !strings.Contains(result.Content, "FR-OPS") {
		t.Error("expected node name in content")
	}
	if !strings.Contains(result.Content, "Budget must stop") {
		t.Error("expected snippet in content")
	}
}

func TestBuilder_Build_NoProjectID(t *testing.T) {
	kbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		frags := []client.KBFragment{
			{DocID: "d1", Snippet: "Test snippet", Score: 0.9},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(frags)
	}))
	defer kbSrv.Close()

	b := NewBuilder(
		client.NewSemanticClient("http://unused:9999"),
		client.NewKnowledgeClient(kbSrv.URL),
	)

	// Empty projectID skips semantic.
	result, err := b.Build(context.Background(), "", "some task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Content, "Semantic Context") {
		t.Error("should not contain semantic section when projectID is empty")
	}
}

func TestBuilder_Build_KBUnavailable(t *testing.T) {
	semSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.SemNode{})
	}))
	defer semSrv.Close()

	b := NewBuilder(
		client.NewSemanticClient(semSrv.URL),
		client.NewKnowledgeClient("http://unreachable:9999"),
	)

	result, err := b.Build(context.Background(), "p1", "task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "unavailable") {
		t.Error("expected 'unavailable' when KB is down")
	}
}

func TestBuilder_Build_Truncation(t *testing.T) {
	kbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a very large response.
		large := strings.Repeat("x", 50_000)
		frags := []client.KBFragment{
			{DocID: "d1", Snippet: large, Score: 0.9},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(frags)
	}))
	defer kbSrv.Close()

	b := NewBuilder(
		client.NewSemanticClient("http://unused:9999"),
		client.NewKnowledgeClient(kbSrv.URL),
	)

	result, err := b.Build(context.Background(), "", "task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Content, "truncated") {
		t.Error("expected truncation marker")
	}
	if len(result.Content) > maxContextBytes+100 {
		t.Errorf("content too large: %d bytes", len(result.Content))
	}
}
