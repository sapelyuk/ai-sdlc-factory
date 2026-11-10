package agent

import (
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
)

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
