package ai_sdlc_factory.autonomy

import future.keywords.if
import future.keywords.in

import data.ai_sdlc_factory.risk_levels

# Tool Gateway authorization policy (issue #10).
#
# Input document:
#   role            - X-Agent-Role header
#   tool            - MCP tool name
#   tool_risk_level - risk level declared by the MCP schema
#   autonomy_level  - X-Autonomy-Level header (R0-R3)
#   author_id       - agent or human that initiated the call
#   approver_id     - X-Approver-ID header (required for R3)
#   role_tools      - tool allow-list from the role schema (optional)
#   role_deny       - deny-list from the role schema (optional)
#
# Output document: {allow, risk_level, reason}

default allow := false
default risk_level := "R0"

# ── Role aliases ──────────────────────────────────────────────

role_aliases := {
	"developer": "software-engineer",
	"software-engineer": "software-engineer",
	"qa": "qa-engineer",
	"qa-engineer": "qa-engineer",
	"reviewer": "code-reviewer",
	"code-reviewer": "code-reviewer",
	"devops": "devops-engineer",
	"devops-engineer": "devops-engineer",
	"admin": "admin",
	"viewer": "viewer",
}

normalized_role := role_aliases[input.role] if {
	input.role in object.keys(role_aliases)
}

# ── Role x tool matrix ───────────────────────────────────────
#
# Deny always wins over allow. A tool that is not listed for a role is
# denied, even if the role schema accidentally lists it.

role_tools := {
	"software-engineer": {
		"git.clone", "git.branch", "git.commit", "git.push", "git.open_pr",
		"git.diff", "git.read", "sandbox.exec", "fs.read", "fs.write",
		"fs.delete", "lsp.diagnostics", "test.run", "tracker.comment",
		"kb.search", "slpr.search", "sem.query",
	},
	"qa-engineer": {
		"git.clone", "git.read", "sandbox.exec", "test.run", "test.report",
		"bug.open", "pr.block", "pr.comment", "kb.search", "slpr.search",
		"sem.query",
	},
	"code-reviewer": {
		"git.diff", "git.read", "pr.review", "pr.approve",
		"pr.request_changes", "sem.query", "kb.search", "slpr.search",
	},
	"devops-engineer": {
		"ci.trigger", "ci.status", "deploy.staging", "deploy.prod",
		"deploy.rollback", "git.read", "kb.search",
	},
	"viewer": {
		"git.diff", "git.read", "fs.read", "lsp.diagnostics", "kb.search",
		"slpr.search", "sem.query", "ci.status",
	},
}

# Explicit deny-lists that cannot be overridden by a schema.
static_deny := {
	"software-engineer": {"deploy.prod", "secrets.read", "git.push_protected"},
	"qa-engineer": {"deploy.prod", "secrets.read", "git.push_protected"},
	"code-reviewer": {"deploy.prod", "secrets.read", "git.push_protected"},
	"devops-engineer": {"secrets.read", "git.push_protected"},
	"viewer": {"deploy.prod", "secrets.read", "git.push_protected"},
}

role_allowed if {
	normalized_role == "admin"
}

role_allowed if {
	input.tool in role_tools[normalized_role]
	catalog_allows
}

# An absent or empty catalog list means the role is not described by a
# schema (for example "admin"); the static matrix remains the only
# restriction.
catalog_allows if {
	not input.role_tools
}

catalog_allows if {
	count(input.role_tools) == 0
}

catalog_allows if {
	input.tool in input.role_tools
}

denied if {
	input.tool in input.role_deny
}

denied if {
	input.tool in static_deny[normalized_role]
}

# ── Risk levels (ADR-013) ────────────────────────────────────

risk_rank := {"R0": 0, "R1": 1, "R2": 2, "R3": 3}

declared_risk := input.tool_risk_level if {
	input.tool_risk_level in {"R0", "R1", "R2", "R3"}
}

declared_risk := "R0" if {
	not input.tool_risk_level in {"R0", "R1", "R2", "R3"}
}

computed_risk := higher_risk(risk_levels.tool_risk_level, risk_levels.action_risk_level)

risk_level := higher_risk(declared_risk, computed_risk)

higher_risk(a, b) = a if {
	risk_rank[a] >= risk_rank[b]
}

higher_risk(a, b) = b if {
	risk_rank[a] < risk_rank[b]
}

autonomy_rank := risk_rank[input.autonomy_level]

autonomy_allows if {
	risk_level in {"R0", "R1"}
}

autonomy_allows if {
	risk_level == "R2"
	autonomy_rank >= 2
}

autonomy_allows if {
	risk_level == "R3"
	approved
}

# R3 is irreversible: it always requires a human approver who is not the
# author of the action (ADR-013).
approved if {
	input.approver_id != ""
	input.approver_id != input.author_id
}

allow if {
	role_allowed
	not denied
	autonomy_allows
}

# ── Decision ─────────────────────────────────────────────────

reason := "allowed" if {
	allow
}

reason := "role is not allowed to call this tool" if {
	not role_allowed
}

reason := "tool is explicitly denied for this role" if {
	role_allowed
	denied
}

reason := "risk level R2 exceeds the autonomy profile" if {
	role_allowed
	not denied
	risk_level == "R2"
	not autonomy_allows
}

reason := "R3 action requires approval by a different human" if {
	role_allowed
	not denied
	risk_level == "R3"
	not autonomy_allows
}

decision := {"allow": allow, "risk_level": risk_level, "reason": reason}

