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
