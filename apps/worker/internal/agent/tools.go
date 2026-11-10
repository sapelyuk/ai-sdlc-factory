package agent

import (
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
)

var readOnlyToolNames = map[string]struct{}{
	"fs.read":            {},
	"fs.list":            {},
	"kb.search":          {},
	"kb.read":            {},
	"sem.query":          {},
	"slpr.search":        {},
	"lsp.diagnostics":    {},
	"web.search":         {},
	"web.fetch":          {},
	"research.summarize": {},
	"figma.read":         {},
}

// isToolAllowed checks whether a tool call is permitted by the role's
// allow-list (defense-in-depth: MCP_TOOLS.md §1, SYSTEM_DESIGN.md §10.2).
//
// Fail-closed: if the allow-list is empty or nil, no tool is permitted.
// This prevents LLM hallucination from bypassing the Tool Gateway filter.
func isToolAllowed(tools []client.ToolDef, name string) bool {
	if tools == nil {
		return false
	}
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// FilterReadOnlyTools returns only read-only tools from the input list.
// Used by the sanitize phase: the LLM must not be able to call write tools
// while processing untrusted handoff content.
func FilterReadOnlyTools(tools []client.ToolDef) []client.ToolDef {
	out := make([]client.ToolDef, 0, len(tools))
	for _, t := range tools {
		if _, ok := readOnlyToolNames[t.Name]; ok {
			out = append(out, t)
		}
	}
	return out
}
