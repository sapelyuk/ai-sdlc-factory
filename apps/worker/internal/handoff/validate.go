package handoff

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

//go:embed handoff.schema.json
var schemaJSON string

// embeddedSchema is the handoff JSON schema loaded at package init.
var embeddedSchema *jsonschema.Schema

func init() {
	s, err := jsonschema.CompileString("handoff.schema.json", string(schemaJSON))
	if err != nil {
		panic(fmt.Sprintf("handoff: compile embedded schema: %v", err))
	}
	embeddedSchema = s
}

// Validator validates handoff packages against the JSON Schema.
type Validator struct {
	schema *jsonschema.Schema
}

// NewValidator returns the pre-compiled handoff validator.
func NewValidator() (*Validator, error) {
	return &Validator{schema: embeddedSchema}, nil
}

// Validate checks the handoff package against the JSON Schema.
// Returns a descriptive error if validation fails.
func (v *Validator) Validate(pkg *HandoffPackage) error {
	data, err := json.Marshal(pkg)
	if err != nil {
		return fmt.Errorf("handoff: marshal package: %w", err)
	}

	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("handoff: unmarshal package: %w", err)
	}

	if err := v.schema.Validate(value); err != nil {
		return fmt.Errorf("handoff: validation failed: %w", err)
	}
	return nil
}

// ValidateJSON validates raw JSON bytes against the schema.
func (v *Validator) ValidateJSON(raw json.RawMessage) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("handoff: decode JSON: %w", err)
	}
	if err := v.schema.Validate(value); err != nil {
		return fmt.Errorf("handoff: validation failed: %w", err)
	}
	return nil
}
