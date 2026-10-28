package context

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestBuilder_Build_PrioritizesDirectNeighbors verifies that the byte budget
// is spent on direct neighbours first, then depth 2, and only then on
// knowledge fragments. A huge depth-2 section must not push out the direct
// neighbours, and KB must be dropped entirely.
func TestBuilder_Build_PrioritizesDirectNeighbors(t *testing.T) {
	var (
		roots  []string
		depths []string
	)
	semSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root := r.URL.Query().Get("root")
		depth := r.URL.Query().Get("depth")
		roots = append(roots, root)
		depths = append(depths, depth)

		var nodes []client.SemNode
		switch depth {
		case "1":
			nodes = []client.SemNode{{ID: "direct", Kind: "Role", Label: strings.Repeat("d", 20_000)}}
		case "2":
			nodes = []client.SemNode{{ID: "expanded", Kind: "Role", Label: strings.Repeat("e", 20_000)}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(nodes)
	}))
	defer semSrv.Close()

	kbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		frags := []client.KBFragment{
			{DocID: "d1", Snippet: strings.Repeat("k", 20_000), Score: 0.9},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(frags)
	}))
	defer kbSrv.Close()

	b := NewBuilder(
		client.NewSemanticClient(semSrv.URL),
		client.NewKnowledgeClient(kbSrv.URL),
	)

	result, err := b.Build(context.Background(), "0190f0e0-0000-7000-8000-000000000001", "task")
	require.NoError(t, err)

	assert.Equal(t, []string{"1", "2"}, depths, "direct neighbours must be fetched before depth 2")
	assert.Equal(t, []string{
		"0190f0e0-0000-7000-8000-000000000001",
		"0190f0e0-0000-7000-8000-000000000001",
	}, roots)
	assert.Contains(t, result.Content, "Semantic Context (direct neighbors)")
	assert.Contains(t, result.Content, `"id":"direct"`)
	assert.Contains(t, result.Content, "truncated")
	assert.NotContains(t, result.Content, "## Knowledge", "KB must be dropped before semantic content")
	assert.LessOrEqual(t, len(result.Content), maxContextBytes)
}

// TestBuilder_Build_DeduplicatesDirectNeighbors ensures a node that is both a
// direct neighbour and part of the depth-2 subgraph is rendered once.
func TestBuilder_Build_DeduplicatesDirectNeighbors(t *testing.T) {
	semSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nodes := []client.SemNode{{ID: "n1", Kind: "Role", Label: "Shared"}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(nodes)
	}))
	defer semSrv.Close()

	kbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.KBFragment{})
	}))
	defer kbSrv.Close()

	b := NewBuilder(
		client.NewSemanticClient(semSrv.URL),
		client.NewKnowledgeClient(kbSrv.URL),
	)

	result, err := b.Build(context.Background(), "root", "task")
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(result.Content, `"id":"n1"`))
}

func TestRenderSections_TruncatesUTF8Safely(t *testing.T) {
	// 3-byte runes: a naive byte cut would split the last one.
	body := strings.Repeat("€", 10)

	out := renderSections([]section{{title: "## T", body: body}}, len("## T\n")+7+len(truncationMarker))
	assert.True(t, strings.HasSuffix(out, truncationMarker))
	assert.NotContains(t, out, "\ufffd")
	assert.LessOrEqual(t, len(out), len("## T\n")+7+len(truncationMarker))
}
