# JSON-Schema files for Registry

This directory contains JSON-Schema files (Draft 2020-12) for validating Registry specifications.

## Files

| File | Description |
|---|---|
| `role.schema.json` | Role specification (agent template) |
| `team.schema.json` | Team specification (agent collection) |
| `process.schema.json` | Process specification (SDLC workflow) |
| `handoff.schema.json` | Handoff package between agents (Issue #104, SYSTEM_DESIGN.md §8.4) |

## Validation

All schemas use `additionalProperties: false` for strict validation.
Required fields are listed in the `required` array.

Usage in Go:
```go
import "github.com/santhosh-tekuri/jsonschema/v5"

schema, _ := jsonschema.CompileString("file://packages/contracts/schemas/role.schema.json")
if err := schema.Validate(data); err != nil {
    // validation error
}
```
