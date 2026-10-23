# ai-sdlc-factory Architecture Diagrams

A set of architecture diagrams in **draw.io** format (`.drawio`).

## Contents

| File | C4 Level | Section | Description |
|---|---|---|---|
| `01-system-context.drawio` | Level 1 — Context | §4.1 | Platform and external systems |
| `02-container-diagram.drawio` | Level 2 — Containers | §4.2 | Three planes and services |
| `03-component-diagram-orchestrator.drawio` | Level 3 — Components | §8 | Internal structure of Orchestrator |

## How to open

### Option 1 — draw.io in browser

1. Open [app.diagrams.net](https://app.diagrams.net).
2. `File → Open` → select the `.drawio` file.

### Option 2 — VS Code

1. Install the `hediet.vscode-drawio` extension.
2. Open the `.drawio` file.

## Naming

`NN-<kind>-<subject>.drawio`
- `NN` — sequential number (01, 02, ...)
- `kind` ∈ {`context`, `container`, `component`, `sequence`, `deployment`, `er`}

## Rules for working with diagrams

1. When updating architecture in `SYSTEM_DESIGN.md` — update the diagram **in the same PR**.
2. Changes to `.drawio` files are reviewed as code.
3. If a diagram contradicts `SYSTEM_DESIGN.md` — the document takes precedence.

## Export for GitHub

1. Open `.drawio` in draw.io.
2. `File → Export as → SVG`.
3. Save to `docs/architecture/diagrams/exports/<name>.svg`.

## Document changes

| Version | Date | Changes |
|---|---|---|
| 1.0 | 2026-10-16 | Initial description |
