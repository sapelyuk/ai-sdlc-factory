# Shared audit log

`packages/sdk-go/audit` provides the workspace-scoped, tamper-evident audit
logger used by platform services. Events are redacted before storage and
appended to the global `audit_log` table with a SHA-256 hash chain.

## Write an event

Create a logger with the service's PostgreSQL pool and log an event with its
workspace, actor, action, and a stable idempotency key when retries are
possible:

```go
logger := audit.NewLogger(pool)
err := logger.Log(ctx, audit.Event{
    WorkspaceID:    workspaceID,
    ActorID:        audit.SystemActor("example-service"),
    Action:         audit.ActionRunStarted,
    IdempotencyKey: "run-started:" + runID.String(),
    ResourceID:     runID.String(),
    Details:        map[string]any{"profile": profile},
})
```

The key is unique per workspace. Repeated writes with the same key return the
existing record instead of extending the chain. Callers should use a stable
key for one logical event and a different key for each distinct event.

## Verify the chain

`Verify` reads the workspace's records and checks each SHA-256 digest and
`prev_hash` link:

```go
if err := logger.Verify(ctx, workspaceID); err != nil {
    return fmt.Errorf("verify audit chain: %w", err)
}
```

PostgreSQL appends run in a transaction, scope RLS to the workspace, acquire a
per-workspace advisory lock, and lock the current chain head with `FOR UPDATE`.
Migration `0014_global_audit_log` adds the hash-chain and idempotency columns.

## Migration guard

`audit_log` is created by migration `0007`, and `0014` adds the `NOT NULL`
`hash` column on top of it. The hash chain can only start from an empty table,
so `0014` aborts when `audit_log` already contains rows instead of backfilling
hashes that would not belong to a verifiable chain. On a fresh database the
migration applies cleanly; to re-apply it after a partial run, remove the
existing rows first.

## Redaction

`Details` are passed through `Redact` before they are hashed and stored, so a
secret never reaches the log. Keys whose name denotes secret material are
replaced wholesale, and string values are screened for known credential
formats: AWS access key IDs, GitHub (`ghp_…`, `github_pat_…`), GitLab
(`glpat-…`), Slack, JWT, PEM private keys, DSN passwords, and OpenAI/Anthropic
API keys (`sk-…`, `sk-proj-…`, `sk-ant-…`).

