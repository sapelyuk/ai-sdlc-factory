// Package context builds the agent context from semantic and knowledge services.
package context

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
)

const (
	// maxContextBytes is the approximate byte budget for context content (~30KB).
	maxContextBytes = 30_000
	// directNeighborDepth fetches only the direct neighbours of the task root.
	directNeighborDepth = 1
	// expandedDepth fetches the wider subgraph used after direct neighbours.
	expandedDepth = 2
	// truncationMarker replaces content that did not fit into the budget.
	truncationMarker = "\n\n[...truncated...]"
)

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

// section is one prioritized block of context. Sections are rendered in order
// and truncated only when the byte budget is exhausted, so earlier (higher
// priority) sections always survive.
type section struct {
	title string
	body  string
}

// Build gathers semantic subgraph and knowledge fragments for the task.
// projectID may be empty (then semantic lookup is skipped).
//
// Priority when the context exceeds maxContextBytes:
//  1. semantic direct neighbours (depth=1) of the project root,
//  2. the expanded subgraph (depth=2),
//  3. knowledge fragments.
func (b *Builder) Build(ctx context.Context, projectID, taskDescription string) (*BuildResult, error) {
	var sections []section

	if projectID != "" {
		sections = append(sections, b.semanticSections(ctx, projectID)...)
	}
	sections = append(sections, b.knowledgeSections(ctx, taskDescription)...)

	return &BuildResult{Content: renderSections(sections, maxContextBytes)}, nil
}

// semanticSections fetches the direct neighbours first and the expanded
// subgraph second, removing nodes already present in the direct section.
func (b *Builder) semanticSections(ctx context.Context, projectID string) []section {
	var sections []section

	direct, directErr := b.semantic.GetSubgraph(ctx, projectID, directNeighborDepth)
	if directErr != nil {
		sections = append(sections, section{
			title: "## Semantic Context (direct neighbors)",
			body:  "(unavailable: " + directErr.Error() + ")",
		})
		return sections
	}
	sections = append(sections, nodeSection("## Semantic Context (direct neighbors)", direct))

	seen := make(map[string]bool, len(direct))
	for _, n := range direct {
		seen[n.ID] = true
	}

	expanded, expandedErr := b.semantic.GetSubgraph(ctx, projectID, expandedDepth)
	if expandedErr != nil {
		sections = append(sections, section{
			title: "## Semantic Context (depth 2)",
			body:  "(unavailable: " + expandedErr.Error() + ")",
		})
		return sections
	}
	sections = append(sections, nodeSection("## Semantic Context (depth 2)", withoutSeen(expanded, seen)))

	return sections
}

// knowledgeSections returns one section per fragment so that higher-scored
// fragments survive truncation.
func (b *Builder) knowledgeSections(ctx context.Context, taskDescription string) []section {
	frags, err := b.knowledge.Search(ctx, taskDescription, 5)
	if err != nil {
		return []section{{title: "## Knowledge", body: "(unavailable: " + err.Error() + ")"}}
	}

	sections := make([]section, 0, len(frags))
	for i, f := range frags {
		title := "## Knowledge"
		if i > 0 {
			title = "## Knowledge (continued)"
		}
		var body strings.Builder
		fmt.Fprintf(&body, "### Fragment %d (score=%.3f)\n%s", i+1, f.Score, f.Snippet)
		sections = append(sections, section{title: title, body: body.String()})
	}
	return sections
}

// nodeSection serializes nodes into a JSON markdown block.
func nodeSection(title string, nodes []client.SemNode) section {
	if len(nodes) == 0 {
		return section{title: title}
	}
	data, err := json.Marshal(nodes)
	if err != nil {
		return section{title: title, body: "(serialization error)"}
	}
	return section{title: title, body: "```json\n" + string(data) + "\n```"}
}

// withoutSeen drops nodes whose ID was already rendered in a higher-priority
// section.
func withoutSeen(nodes []client.SemNode, seen map[string]bool) []client.SemNode {
	out := make([]client.SemNode, 0, len(nodes))
	for _, n := range nodes {
		if seen[n.ID] {
			continue
		}
		out = append(out, n)
	}
	return out
}

// renderSections joins sections in priority order within the byte limit.
// When a section does not fit, it is truncated and all lower-priority
// sections are dropped.
func renderSections(sections []section, limit int) string {
	var sb strings.Builder
	for _, s := range sections {
		if s.body == "" {
			continue
		}
		separator := ""
		if sb.Len() > 0 {
			separator = "\n\n"
		}
		block := s.title + "\n" + s.body
		if sb.Len()+len(separator)+len(block) <= limit {
			sb.WriteString(separator)
			sb.WriteString(block)
			continue
		}

		header := s.title + "\n"
		remaining := limit - sb.Len() - len(separator) - len(truncationMarker)
		if remaining <= len(header) {
			break
		}
		sb.WriteString(separator)
		sb.WriteString(header)
		sb.WriteString(truncateUTF8(s.body, remaining-len(header)))
		sb.WriteString(truncationMarker)
		return sb.String()
	}
	return sb.String()
}

// truncateUTF8 cuts s to at most maxBytes without splitting a rune.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
