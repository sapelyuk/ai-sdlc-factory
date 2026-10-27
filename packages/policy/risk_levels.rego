package ai_sdlc_factory.risk_levels

import future.keywords.if
import future.keywords.in

# Risk scoring based on task context
# Used by OPA for policy-as-code checks (ADR-013)

default risk_score := 0
default risk_category := "low"

# Critical operations
risk_score := 10 if {
    input.category == "deployment"
    input.target == "production"
}

risk_score := 9 if {
    input.category == "secrets"
    input.action == "rotate"
}

# High risk
risk_score := 7 if {
    input.category == "deployment"
    input.target == "staging"
}

risk_score := 6 if {
    input.category == "infrastructure"
    input.action == "modify"
}

# Medium risk
risk_score := 4 if {
    input.category == "code"
    input.action == "merge"
}

risk_score := 3 if {
    input.category == "documentation"
}

# Low risk
risk_score := 1 if {
    input.category == "read"
}

risk_score := 2 if {
    input.category == "development"
}

# Category derived from the score
risk_category := "critical" if {
    risk_score >= 9
}

risk_category := "high" if {
    risk_score >= 6
    risk_score < 9
}

risk_category := "medium" if {
    risk_score >= 3
    risk_score < 6
}

risk_category := "low" if {
    risk_score < 3
}

# ── Tool risk classification (ADR-013) ────────────────────────
#
# Used by the Tool Gateway (issue #10). The final risk level of a call is
# the higher of the schema-declared level and this classification, so a
# schema cannot silently downgrade a dangerous tool.

default tool_risk_level := "R0"

r3_tools := {
    "deploy.prod", "destroy.prod", "rotate.secrets", "delete.workspace",
    "merge.main", "secrets.read", "git.push_protected",
}

r2_tools := {
    "merge.release", "update.production.config", "rotate.api.keys",
    "create.production.resource", "deploy.staging", "deploy.rollback",
    "slpr.propose_node", "slpr.propose_edge", "figma.write", "sem.am_write",
    "adr.propose", "diag.publish",
}

r1_tools := {
    "git.branch", "git.commit", "git.push", "git.open_pr", "sandbox.exec",
    "fs.write", "fs.delete", "test.run", "tracker.comment", "bug.open",
    "pr.block", "pr.comment", "pr.review", "pr.approve",
    "pr.request_changes", "ci.trigger", "test.report", "kb.write_draft",
    "design.export_tokens", "diag.render", "diag.validate", "sast.run",
    "dep.scan", "secret.scan", "agent.handoff", "pm.create_prd",
    "pm.update_backlog",
}

tool_risk_level := "R3" if {
    input.tool in r3_tools
}

tool_risk_level := "R2" if {
    input.tool in r2_tools
}

tool_risk_level := "R1" if {
    input.tool in r1_tools
}

# Legacy action-based classification kept for non-MCP callers.
default action_risk_level := "R0"

action_risk_level := "R3" if {
    input.action in {"deploy.prod", "destroy.prod", "rotate.secrets", "delete.workspace", "merge.main"}
}

action_risk_level := "R2" if {
    input.action in {"merge.release", "update.production.config", "rotate.api.keys", "create.production.resource"}
}

action_risk_level := "R1" if {
    input.action in {"create.branch", "commit.local", "update.pr", "create.issue", "edit.docs"}
}
