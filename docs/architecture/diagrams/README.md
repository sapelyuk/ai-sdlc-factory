# Диаграммы архитектуры ai-sdlc-factory

Набор архитектурных диаграмм в формате **draw.io** (`.drawio`).

## Состав

| Файл | C4-уровень | Раздел | Описание |
|---|---|---|---|
| `01-system-context.drawio` | Level 1 — Context | §4.1 | Платформа и внешние системы |
| `02-container-diagram.drawio` | Level 2 — Containers | §4.2 | Три плоскости и сервисы |
| `03-component-diagram-orchestrator.drawio` | Level 3 — Components | §8 | Внутренняя структура Orchestrator |

## Как открыть

### Вариант 1 — draw.io в браузере

1. Откройте [app.diagrams.net](https://app.diagrams.net).
2. `File → Open` → выберите `.drawio` файл.

### Вариант 2 — VS Code

1. Установите расширение `hediet.vscode-drawio`.
2. Откройте `.drawio` файл.

## Нейминг

`NN-<kind>-<subject>.drawio`
- `NN` — порядковый номер (01, 02, ...)
- `kind` ∈ {`context`, `container`, `component`, `sequence`, `deployment`, `er`}

## Правила работы с диаграммами

1. При изменении архитектуры в `SYSTEM_DESIGN.md` — обновить диаграмму **в том же PR**.
2. Изменения в `.drawio` ревьюются как код.
3. Если диаграмма противоречит `SYSTEM_DESIGN.md` — приоритет у документа.

## Экспорт для GitHub

1. Откройте `.drawio` в draw.io.
2. `File → Export as → SVG`.
3. Сохраните в `docs/architecture/diagrams/exports/<name>.svg`.

## Изменения документа

| Версия | Дата | Изменения |
|---|---|---|
| 1.0 | 16.10.2026 | Первичное описание |
