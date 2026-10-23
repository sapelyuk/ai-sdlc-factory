package ai_sdlc_factory.autonomy

import future.keywords.if

# Risk levels of agent actions (ADR-013)
# R0 - read, R1 - local write, R2 - significant, R3 - irreversible

default risk_level := "R0"

# R3 - irreversible actions (require human approve)
risk_level := "R3" if {
    action_list := {"deploy.prod", "destroy.prod", "rotate.secrets", "delete.workspace", "merge.main"}
    input.action = action_list[_]
}

# R2 - significant actions (per autonomy profile)
risk_level := "R2" if {
    action_list := {"merge.release", "update.production.config", "rotate.api.keys", "create.production.resource"}
    input.action = action_list[_]
}

# R1 - local write (automatic)
risk_level := "R1" if {
    action_list := {"create.branch", "commit.local", "update.pr", "create.issue", "edit.docs"}
    input.action = action_list[_]
}

# R0 - read (automatic by default)

# Check whether an action is allowed for the given autonomy level
# autonomy_level: "R0" | "R1" | "R2" | "R3"
allow if {
    desired := risk_level
    input.autonomy_level = desired
}

allow if {
    desired := risk_level
    autonomy_rank := rank(input.autonomy_level)
    action_rank := rank(desired)
    autonomy_rank >= action_rank
}

rank("R0") = 0
rank("R1") = 1
rank("R2") = 2
rank("R3") = 3

# TODO: implement the full action matrix in the next issue
