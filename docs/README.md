# Документация ai-sdlc-factory

Репозиторий документации платформы `ai-sdlc-factory` — мультиагентной среды разработки, где команды ИИ-агентов реализуют полный SDLC под контролем владельца.

## Структура

- [`architecture/SYSTEM_DESIGN.md`](architecture/SYSTEM_DESIGN.md) — главный архитектурный документ (v1.2). Точка входа для ИИ-агента-исполнителя.
- [`architecture/ADR.md`](architecture/ADR.md) — реестр архитектурных решений (ADR-001…ADR-018).
- [`architecture/ONTOLOGY_SPEC.md`](architecture/ONTOLOGY_SPEC.md) — спецификация семантического слоя (SLP / SLPr / AM).
- [`architecture/SDLC_STATE_MACHINE.md`](architecture/SDLC_STATE_MACHINE.md) — состояния задач, переходы, гейты, условия блокировки.
- [`architecture/MCP_TOOLS.md`](architecture/MCP_TOOLS.md) — реестр MCP-инструментов по ролям.
- [`AGENT_KICKOFF.md`](AGENT_KICKOFF.md) — инструкция по превращению документации в GitHub Issues.

## Как читать

1. Начните с `SYSTEM_DESIGN.md` — это полная картина.
2. Перед реализацией — прочитайте `ADR.md` (обязательные решения).
3. Для работы над семантическим слоем — `ONTOLOGY_SPEC.md`.
4. Для работы над оркестрацией — `SDLC_STATE_MACHINE.md`.
5. Для работы над Tool Gateway — `MCP_TOOLS.md`.

## Соглашения

- Имя сервиса: **`ai-sdlc-factory`**.
- `apiVersion` YAML: **`ai-sdlc-factory/v1`**.
- Namespace онтологии: **`ai-sdlc-factory/product-domain@^1`**.
- Корневой каталог монорепозитория: **`ai-sdlc-factory/`**.
- Префикс контейнеров и Helm-релизов: **`ai-sdlc-factory-*`**.
- Слова MUST / SHOULD / MAY — в значении RFC 2119.
- Идентификаторы (G, P, FR-*, NFR-*, ADR-*) — стабильны.

## Контрибьюция

- Изменения архитектуры — PR с обновлением соответствующего ADR.
- Изменения семантического слоя — PR с обновлением `ONTOLOGY_SPEC.md` и версии онтологии.
- Изменения процесса — PR с обновлением `SDLC_STATE_MACHINE.md`.
- Каждый PR — ссылки на FR/NFR/ADR.

## Лицензия

AGPLv3 (GNU Affero General Public License v3)
