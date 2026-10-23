# How to contribute to ai-sdlc-factory

## Golden rule: no direct commits to main

Any change follows the same path:

```
issue → branch → PR → main
```

- No branch without an issue.
- No direct commits to `main`.
- No force-push to `main`.
- Branch protection is enforced at the GitHub level.

The only exception is the initial bootstrap commit (PR #1).

## Branch naming

`<type>/<issue-number>-<short-name>`

| Type | When to use |
|---|---|
| `feat/` | New feature |
| `fix/` | Bug fix |
| `chore/` | Infrastructure, tools, dependencies |
| `docs/` | Documentation only |
| `refactor/` | Refactoring without behavior change |
| `test/` | Tests only |
| `spike/` | Time-boxed investigation |

Examples:
- `feat/42-implement-agent-config`
- `fix/107-fix-drift-detector`
- `chore/1-initial-scaffold`
- `docs/88-update-ontology-spec`

## Commit messages

Format: Conventional Commits. Each commit references an issue.

```
<type>(<scope>): <short description> (#<issue>)

<body, optional>

Refs #<issue>
```

Allowed types: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `ci`, `build`, `perf`, `revert`.

Examples:

```
feat(orchestrator): implement sdlc_workflow in Temporal (#42)
fix(semantic): correct pagination in traversal API (#107)
chore(deps): bump temporal-sdk to v1.28 (#88)
docs(adr): add ADR-019 for event-bus abstraction (#95)
```

## Language policy

**All content in this repository MUST be in English.** This includes:
- Documentation (`.md` files)
- Commit messages
- Pull request titles and descriptions
- Issue titles and descriptions
- Code comments (where applicable)
- YAML/JSON configuration comments

Non-English content (including Cyrillic) is **not allowed**. A CI check enforces this policy.
If you need to discuss a change in another language, do it in the PR comments (not in the codebase).

## Pull Requests

- Open PRs as **draft** immediately after the first commit.
- Reference the issue: `Closes #N` in the PR body.
- Keep PRs small: **≤ 400 lines of diff** where possible.
- All CI checks must pass.
- Fill in the PR template completely.
- PR description MUST be in English.
- Merging without approval is forbidden (except self-review for solo projects).

**Test deletion prohibition:** Within a PR, it is forbidden to delete, skip (`t.Skip`/`xit`), or weaken existing tests, or to change CI coverage thresholds to bypass errors. If a test is outdated — this is handled in a separate PR labeled `type:refactor` and reviewed by the architect.

## Merge strategy

- **Squash merge** into `main`.
- Branch deletion after merge — enabled.
- Merged commit message = PR title.

## What to do if CI fails

- Fix with a new commit, not via `--amend` or force-push.
- Each new commit references the same issue.
- Re-run CI by pushing the new commit.

## Working with AI agents

AI agents follow the same rules.

- How to plan work — `docs/AGENT_KICKOFF.md`.
- How agents work on code — `docs/architecture/SYSTEM_DESIGN.md` §16.1.
- Naming rules and ADR workflow — same §16.1.

## Protected branches

`main` is protected:

- PR required before merge.
- Required passing checks: `ci`.
- All PR discussions must be resolved.
- Force-push forbidden.
- Branch deletion forbidden.

## Where to find what

| Question | File |
|---|---|
| How to plan work | `docs/AGENT_KICKOFF.md` |
| Architecture | `docs/architecture/SYSTEM_DESIGN.md` |
| Architecture decisions | `docs/architecture/ADR.md` |
| Semantic layer | `docs/architecture/ONTOLOGY_SPEC.md` |
| Task states and gates | `docs/architecture/SDLC_STATE_MACHINE.md` |
| Agent MCP tools | `docs/architecture/MCP_TOOLS.md` |
| Local startup | `deploy/compose/README.md` |

## Naming and conventions

- Service name: **`ai-sdlc-factory`**.
- `apiVersion` YAML: **`ai-sdlc-factory/v1`**.
- Ontology namespace: **`ai-sdlc-factory/product-domain@^1`**.
- Monorepo root directory: **`ai-sdlc-factory/`**.
- Container and Helm release prefix: **`ai-sdlc-factory-*`**.
- MUST / SHOULD / MAY — per RFC 2119.
- Identifiers (G, P, FR-*, NFR-*, ADR-*) are stable.

## License

This project is licensed under AGPLv3. By submitting a PR, you agree to the terms of this license.