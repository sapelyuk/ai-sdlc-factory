package ai_sdlc_factory.risk_levels

import future.keywords.if

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

# TODO: implement the full risk matrix in the next issue
