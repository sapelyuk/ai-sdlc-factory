// Package context builds the agent context from semantic and knowledge services.
package context

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
)

// maxContextBytes is the approximate byte budget for context content (~30KB).
const maxContextBytes = 30_000

// Builder assembles context for the agent from semantic and knowledge sources.
type Builder struct {
	semantic  *client.SemanticClient
	knowledge *client.KnowledgeClient
}

// NewBuilder creates a new Context Builder.
func NewBuilder(semantic *client.SemanticClient, knowledge *client.KnowledgeClient) *Builder {
	return &Builder{
		semantic:  semantic,
		knowledge: knowledge,
	}
}

// BuildResult contains the assembled context as a markdown string.
type BuildResult struct {
	Content string
}

// Build gathers semantic subgraph and knowledge fragments for the task.
// projectID may be empty (then semantic lookup is skipped).
func (b *Builder) Build(ctx context.Context, projectID, taskDescription string) (*BuildResult, error) {
	var parts []string

	// Fetch semantic subgraph if projectID is provided.
	if projectID != "" {
		nodes, err := b.semantic.GetSubgraph(ctx, projectID, 2)
		if err != nil {
			// Non-fatal: log and continue without semantic context.
			parts = append(parts, "## Semantic Context\n(unavailable: "+err.Error()+")")
		} else {
			semanticJSON, err := json.Marshal(nodes)
			if err != nil {
				parts = append(parts, "## Semantic Context\n(serialization error)")
			} else {
				parts = append(parts, "## Semantic Context\n```json\n"+string(semanticJSON)+"\n```")
			}
		}
	}

	// Fetch knowledge fragments.
	frags, err := b.knowledge.Search(ctx, taskDescription, 5)
	if err != nil {
		parts = append(parts, "## Knowledge\n(unavailable: "+err.Error()+")")
	} else {
		var sb strings.Builder
		sb.WriteString("## Knowledge\n")
		for i, f := range frags {
			sb.WriteString(fmt.Sprintf("### Fragment %d (score=%.3f)\n%s\n\n", i+1, f.Score, f.Snippet))
		}
		parts = append(parts, sb.String())
	}

	result := strings.Join(parts, "\n\n")

	// Truncate if too large, keeping the beginning (semantic context has priority).
	if len(result) > maxContextBytes {
		result = result[:maxContextBytes] + "\n\n[...truncated...]"
	}

	return &BuildResult{Content: result}, nil
}
