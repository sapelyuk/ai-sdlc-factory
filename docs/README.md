# ai-sdlc-factory Documentation

The `ai-sdlc-factory` documentation repository — a multi-agent development platform where AI agent teams implement the full SDLC under owner control.

## Structure

- [`architecture/SYSTEM_DESIGN.md`](architecture/SYSTEM_DESIGN.md) — main architecture document (v1.2). Entry point for the executor AI agent.
- [`architecture/ADR.md`](architecture/ADR.md) — architecture decision record registry (ADR-001…ADR-018).
- [`architecture/ONTOLOGY_SPEC.md`](architecture/ONTOLOGY_SPEC.md) — semantic layer specification (SLP / SLPr / AM).
- [`architecture/SDLC_STATE_MACHINE.md`](architecture/SDLC_STATE_MACHINE.md) — task states, transitions, gates, and blocking conditions.
- [`architecture/MCP_TOOLS.md`](architecture/MCP_TOOLS.md) — MCP tools registry organized by role.
- [`AGENT_KICKOFF.md`](AGENT_KICKOFF.md) — guide for converting documentation into GitHub Issues.

## How to read

1. Start with `SYSTEM_DESIGN.md` for the full picture.
2. Before implementation — read `ADR.md` (binding decisions).
3. For semantic layer work — `ONTOLOGY_SPEC.md`.
4. For orchestration work — `SDLC_STATE_MACHINE.md`.
5. For Tool Gateway work — `MCP_TOOLS.md`.

## Conventions

- Service name: **`ai-sdlc-factory`**.
- `apiVersion` YAML: **`ai-sdlc-factory/v1`**.
- Ontology namespace: **`ai-sdlc-factory/product-domain@^1`**.
- Monorepo root directory: **`ai-sdlc-factory/`**.
- Container and Helm release prefix: **`ai-sdlc-factory-*`**.
- MUST / SHOULD / MAY — per RFC 2119.
- Identifiers (G, P, FR-*, NFR-*, ADR-*) are stable.

## Contribution

- Architecture changes — PR with corresponding ADR update.
- Semantic layer changes — PR with `ONTOLOGY_SPEC.md` update and ontology version bump.
- Process changes — PR with `SDLC_STATE_MACHINE.md` update.
- Every PR — references to FR/NFR/ADR.

## License

AGPLv3 (GNU Affero General Public License v3)
