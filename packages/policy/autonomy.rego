package ai_sdlc_factory.autonomy

import future.keywords.if

# Уровни риска действий агентов (ADR-013)
# R0 — чтение, R1 — локальная запись, R2 — значимое, R3 — необратимое

default risk_level := "R0"

# R3 — необратимые действия (требуют approve человека)
risk_level := "R3" if {
    action_list := {"deploy.prod", "destroy.prod", "rotate.secrets", "delete.workspace", "merge.main"}
    input.action = action_list[_]
}

# R2 — значимые действия (по профилю автономности)
risk_level := "R2" if {
    action_list := {"merge.release", "update.production.config", "rotate.api.keys", "create.production.resource"}
    input.action = action_list[_]
}

# R1 — локальная запись (авто)
risk_level := "R1" if {
    action_list := {"create.branch", "commit.local", "update.pr", "create.issue", "edit.docs"}
    input.action = action_list[_]
}

# R0 — чтение (авто по умолчанию)

# Проверка разрешённости действия по уровню автономности
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

# TODO: реализовать полную матрицу действий в Issue #<следующий>
