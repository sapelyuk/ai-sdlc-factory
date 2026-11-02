package handoff

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

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
// The runtime metadata header is stripped first: handoff.schema.json has
// additionalProperties=false and metadata is a routing header, not contract
// data, so a stamped package still validates exactly like its wire form.
// Returns a descriptive error if validation fails.
func (v *Validator) Validate(pkg *HandoffPackage) error {
	wire, err := WireCopy(pkg)
	if err != nil {
		return err
	}
	data, err := json.Marshal(wire)
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

// ValidateIncoming checks the incoming handoff package for completeness.
// Returns a list of missing fields rather than a single error, so the
// receiver can return the package to the sender with precise diagnostics
// (SYSTEM_DESIGN.md §8.4: the receiver MUST NOT guess).
func ValidateIncoming(pkg *HandoffPackage) (missing []string, err error) {
	if pkg == nil {
		return nil, fmt.Errorf("handoff: incoming package is nil")
	}
	missing = []string{}
	if strings.TrimSpace(pkg.Goal) == "" {
		missing = append(missing, "goal")
	}
	if len(pkg.AcceptanceCriteria) < 1 {
		missing = append(missing, "acceptance_criteria")
	}
	if len(pkg.ContextRefs) == 0 {
		missing = append(missing, "context_refs")
	} else {
		for i, ref := range pkg.ContextRefs {
			if !isAcceptedURI(ref) {
				missing = append(missing, fmt.Sprintf("context_refs[%d] invalid URI scheme", i))
			}
		}
	}
	if pkg.ExpectedOutputSchema == nil || len(pkg.ExpectedOutputSchema) == 0 {
		missing = append(missing, "expected_output_schema")
	} else {
		var parsed map[string]any
		if err := json.Unmarshal(pkg.ExpectedOutputSchema, &parsed); err != nil {
			missing = append(missing, "expected_output_schema")
		}
	}
	return missing, nil
}

// isAcceptedURI checks if the reference is a valid URI with an accepted scheme.
func isAcceptedURI(ref string) bool {
	u, err := url.Parse(ref)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "sem", "kb", "git", "artifact":
		return true
	}
	return false
}
