// Package catalog loads MCP tool schemas from packages/contracts/mcp-schemas.
//
// Every JSON file describes the tool allow-list of one role. The catalog is
// loaded once at startup and fails fast on a malformed file: an agent must
// never run with a partially loaded tool set.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
)

// SchemaVersion is the only schema version understood by the gateway.
const SchemaVersion = "ai-sdlc-factory/v1"

// Tool is one MCP tool definition from a schema file.
type Tool struct {
	Name             string          `json:"name"`
	Description      string          `json:"description"`
	InputSchema      json.RawMessage `json:"input_schema"`
	OutputSchema     json.RawMessage `json:"output_schema,omitempty"`
	RiskLevel        string          `json:"risk_level"`
	Idempotent       bool            `json:"idempotent"`
	RequiresApproval string          `json:"requires_approval"`
}

// File is the on-disk representation of a role schema.
type File struct {
	Role          string   `json:"role"`
	SchemaVersion string   `json:"schema_version"`
	Tools         []Tool   `json:"tools"`
	Deny          []string `json:"deny"`
}

// Role is a validated role schema.
type Role struct {
	Name          string
	SchemaVersion string
	Deny          []string
	Tools         []Tool
}

// Catalog is the in-memory tool registry.
type Catalog struct {
	roles map[string]Role
	tools map[string]Tool
	names []string
	files int
}

// Load reads every *.json file in dir, validates it and returns the catalog.
func Load(dir string) (*Catalog, error) {
	if dir == "" {
		return nil, errors.New("schema directory is empty")
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("glob schema files: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no MCP schema files found in %s", dir)
	}
	sort.Strings(paths)

	c := &Catalog{
		roles: make(map[string]Role, len(paths)),
		tools: make(map[string]Tool),
	}

	for _, path := range paths {
		if err := c.loadFile(path); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
	}

	sort.Strings(c.names)
	return c, nil
}

func (c *Catalog) loadFile(path string) error {
	raw, err := os.ReadFile(path) // #nosec G304 -- the path comes from the configured schema directory.
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return fmt.Errorf("parse JSON: %w", err)
	}
	if f.Role == "" {
		return errors.New("role is required")
	}
	if f.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %q, got %q", SchemaVersion, f.SchemaVersion)
	}
	if len(f.Tools) == 0 {
		return errors.New("tools must not be empty")
	}
	if _, exists := c.roles[f.Role]; exists {
		return fmt.Errorf("duplicate role %q", f.Role)
	}

	seen := make(map[string]struct{}, len(f.Tools))
	for i := range f.Tools {
		t := f.Tools[i]
		if err := validateTool(t); err != nil {
			return fmt.Errorf("tool %d (%q): %w", i, t.Name, err)
		}
		if _, dup := seen[t.Name]; dup {
			return fmt.Errorf("duplicate tool %q in role %q", t.Name, f.Role)
		}
		seen[t.Name] = struct{}{}

		if existing, ok := c.tools[t.Name]; ok {
			if existing.RiskLevel != t.RiskLevel {
				return fmt.Errorf("tool %q has conflicting risk levels %s and %s",
					t.Name, existing.RiskLevel, t.RiskLevel)
			}
			continue
		}
		c.tools[t.Name] = t
		c.names = append(c.names, t.Name)
	}

	c.roles[f.Role] = Role{
		Name:          f.Role,
		SchemaVersion: f.SchemaVersion,
		Deny:          append([]string(nil), f.Deny...),
		Tools:         append([]Tool(nil), f.Tools...),
	}
	c.files++
	return nil
}

func validateTool(t Tool) error {
	if t.Name == "" {
		return errors.New("name is required")
	}
	if t.Description == "" {
		return errors.New("description is required")
	}
	if !validRiskLevel(t.RiskLevel) {
		return fmt.Errorf("risk_level must be R0, R1, R2 or R3, got %q", t.RiskLevel)
	}
	if len(t.InputSchema) == 0 || !json.Valid(t.InputSchema) {
		return errors.New("input_schema must be valid JSON")
	}
	var schema struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
		return fmt.Errorf("input_schema: %w", err)
	}
	if schema.Type != "object" {
		return errors.New(`input_schema.type must be "object"`)
	}
	if len(t.OutputSchema) > 0 && !json.Valid(t.OutputSchema) {
		return errors.New("output_schema must be valid JSON")
	}
	return nil
}

func validRiskLevel(level string) bool {
	switch level {
	case "R0", "R1", "R2", "R3":
		return true
	default:
		return false
	}
}

// Files returns the number of loaded schema files.
func (c *Catalog) Files() int { return c.files }

// Len returns the number of unique tools.
func (c *Catalog) Len() int { return len(c.names) }

// Tools returns all unique tools sorted by name.
func (c *Catalog) Tools() []Tool {
	out := make([]Tool, 0, len(c.names))
	for _, name := range c.names {
		out = append(out, c.tools[name])
	}
	return out
}

// Tool looks up a tool by name.
func (c *Catalog) Tool(name string) (Tool, bool) {
	t, ok := c.tools[name]
	return t, ok
}

// Role looks up a role by name.
func (c *Catalog) Role(name string) (Role, bool) {
	r, ok := c.roles[name]
	return r, ok
}

// Roles returns all role names sorted.
func (c *Catalog) Roles() []string {
	out := make([]string, 0, len(c.roles))
	for name := range c.roles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ToolNamesForRole returns the tools allowed by the role schema.
func (c *Catalog) ToolNamesForRole(role string) []string {
	r, ok := c.roles[c.resolveRole(role)]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(r.Tools))
	for _, t := range r.Tools {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}

// DenyForRole returns the explicit deny list of the role schema.
func (c *Catalog) DenyForRole(role string) []string {
	r, ok := c.roles[c.resolveRole(role)]
	if !ok {
		return nil
	}
	return append([]string(nil), r.Deny...)
}

// roleAliases maps short role names used by agents to schema role names.
var roleAliases = map[string]string{
	"developer": "software-engineer",
	"qa":        "qa-engineer",
	"reviewer":  "code-reviewer",
	"devops":    "devops-engineer",
}

func (c *Catalog) resolveRole(role string) string {
	if alias, ok := roleAliases[role]; ok {
		return alias
	}
	return role
}

// LogSummary writes the loaded catalog size to the logger.
func (c *Catalog) LogSummary(logger *slog.Logger) {
	logger.Info("loaded MCP schemas",
		slog.Int("schema_files", c.files),
		slog.Int("tools", len(c.names)),
		slog.Any("roles", c.Roles()),
	)
}
