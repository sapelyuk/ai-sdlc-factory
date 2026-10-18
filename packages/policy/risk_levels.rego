package ai_sdlc_factory.risk_levels

import future.keywords.if

# Оценка риска на основе контекста задачи
# Используется OPA для policy-as-code проверки (ADR-013)

default risk_score := 0
default risk_category := "low"

# Критические операции
risk_score := 10 if {
    input.category == "deployment"
    input.target == "production"
}

risk_score := 9 if {
    input.category == "secrets"
    input.action == "rotate"
}

# Высокий риск
risk_score := 7 if {
    input.category == "deployment"
    input.target == "staging"
}

risk_score := 6 if {
    input.category == "infrastructure"
    input.action == "modify"
}

# Средний риск
risk_score := 4 if {
    input.category == "code"
    input.action == "merge"
}

risk_score := 3 if {
    input.category == "documentation"
}

# Низкий риск
risk_score := 1 if {
    input.category == "read"
}

risk_score := 2 if {
    input.category == "development"
}

# Категория по оценке
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

# TODO: реализовать полную матрицу рисков в Issue #<следующий>
