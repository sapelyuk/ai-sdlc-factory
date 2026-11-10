package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

type roleDocument struct {
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Inputs  []artifactDocument `yaml:"inputs"`
		Outputs []artifactDocument `yaml:"outputs"`
		Tools   struct {
			Allow []string `yaml:"allow"`
			Deny  []string `yaml:"deny"`
		} `yaml:"tools"`
		Autonomy    string `yaml:"autonomy"`
		ToolsPolicy string `yaml:"tools_policy"`
	} `yaml:"spec"`
}

type artifactDocument struct {
	Name         string `yaml:"name"`
	ArtifactType string `yaml:"artifact_type"`
	Type         string `yaml:"type"`
	Required     *bool  `yaml:"required"`
	SchemaRef    string `yaml:"schema_ref"`
}

func (a artifactDocument) artifactTypeName() string {
	if a.ArtifactType != "" {
		return a.ArtifactType
	}
	return a.Type
}

func (a artifactDocument) isRequired() bool {
	if a.Required == nil {
		return true
	}
	return *a.Required
}

func TestRoleSchemaValidatesAllNineRoleFiles(t *testing.T) {
	root := repositoryRoot(t)
	roleDirectory := filepath.Join(root, "catalog", "roles")
	schema := compileRoleSchema(t, filepath.Join(root, "packages", "contracts", "schemas", "role.schema.json"))
	if err := ValidateRoleDirectory(roleDirectory, schema); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(roleDirectory)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".yaml" {
			got = append(got, strings.TrimSuffix(entry.Name(), ".yaml"))
		}
	}
	want := []string{
		"backend-developer",
		"business-analyst",
		"code-reviewer",
		"frontend-developer",
		"product-manager",
		"product-researcher",
		"qa-engineer",
		"software-architect",
		"ux-ui-designer",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("role catalog = %v, want %v", got, want)
	}
}

func TestRoleToolsAndAutonomy(t *testing.T) {
	root := repositoryRoot(t)
	roleDirectory := filepath.Join(root, "catalog", "roles")
	schema := compileRoleSchema(t, filepath.Join(root, "packages", "contracts", "schemas", "role.schema.json"))
	if err := ValidateRoleDirectory(roleDirectory, schema); err != nil {
		t.Fatal(err)
	}

	roles := make(map[string]roleDocument, 9)
	entries, err := os.ReadDir(roleDirectory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(roleDirectory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var role roleDocument
		if err := yaml.Unmarshal(data, &role); err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		roles[role.Metadata.Name] = role
		if len(role.Spec.Inputs) == 0 || len(role.Spec.Outputs) == 0 {
			t.Errorf("%s must define at least one input and output artifact", role.Metadata.Name)
		}
		if role.Spec.Autonomy == "autonomous" {
			t.Errorf("%s uses autonomous autonomy", role.Metadata.Name)
		}
	}

	requireTools(t, roles["software-architect"], "allow", "diag.validate")
	requireTools(t, roles["software-architect"], "deny", "deploy.prod", "secrets.read", "git.push_protected")
	requireTools(t, roles["product-researcher"], "deny", "git.*")
	requireTools(t, roles["backend-developer"], "deny", "deploy.prod", "secrets.read", "git.push_protected")
	requireTools(t, roles["frontend-developer"], "deny", "deploy.prod", "secrets.read", "git.push_protected")
	requireTools(t, roles["qa-engineer"], "allow", "sandbox.exec", "test.run", "test.report", "bug.open")
	requireTools(t, roles["code-reviewer"], "allow", "git.diff", "git.read", "pr.review", "pr.approve")
	requireTools(t, roles["product-manager"], "allow", "pm.create_prd", "pm.update_backlog", "tracker.comment", "kb.search")
	requireTools(t, roles["business-analyst"], "allow", "slpr.propose_node", "slpr.propose_edge", "slpr.search")
	requireTools(t, roles["ux-ui-designer"], "allow", "figma.read", "figma.write", "design.export_tokens")
}

func TestSoftwareArchitectOutputsContainAM(t *testing.T) {
	role := loadRole(t, "software-architect")
	for _, out := range role.Spec.Outputs {
		if out.Name == "am_v1" && out.artifactTypeName() == "am" {
			return
		}
	}
	t.Fatal("software-architect must declare am_v1 with artifact_type am")
}

func TestProductManagerResearchReportOptional(t *testing.T) {
	role := loadRole(t, "product-manager")
	for _, in := range role.Spec.Inputs {
		if in.Name == "research_report" {
			if in.isRequired() {
				t.Fatal("research_report must be required: false for product-manager")
			}
			return
		}
	}
	t.Fatal("product-manager must declare research_report input")
}

// TestCatalogRoles_UseArtifactTypeNotLegacyType guards the catalog against
// drifting to the deprecated `type` field. HTTP API still accepts legacy `type`
// for compatibility, but catalog YAMLs must use `artifact_type`.
func TestCatalogRoles_UseArtifactTypeNotLegacyType(t *testing.T) {
	for _, name := range []string{
		"backend-developer",
		"business-analyst",
		"code-reviewer",
		"frontend-developer",
		"product-manager",
		"product-researcher",
		"qa-engineer",
		"software-architect",
		"ux-ui-designer",
	} {
		role := loadRole(t, name)
		for _, in := range role.Spec.Inputs {
			if in.Type != "" {
				t.Errorf("%s.inputs[%s]: use artifact_type, not deprecated type", role.Metadata.Name, in.Name)
			}
		}
		for _, out := range role.Spec.Outputs {
			if out.Type != "" {
				t.Errorf("%s.outputs[%s]: use artifact_type, not deprecated type", role.Metadata.Name, out.Name)
			}
		}
	}
}

func TestArtifactSchemaAcceptsLegacyTypeAndRejectsMixedFields(t *testing.T) {
	schema := compileRoleSchema(t, filepath.Join(repositoryRoot(t), "packages", "contracts", "schemas", "role.schema.json"))
	legacyRole := "apiVersion: ai-sdlc-factory/v1\nkind: Role\nmetadata:\n  name: legacy-role\n  version: 1.0.0\n  description: Legacy role.\nspec:\n  system_prompt_ref: prompts/roles/legacy-role.md\n  inputs:\n    - name: request\n      type: document\n  outputs:\n    - name: result\n      type: document\n  model:\n    capability: code-fast\n  tools:\n    allow: []\n    deny: []\n  knowledge_scopes: [platform]\n  limits:\n    max_steps: 1\n    max_cost_usd: 0\n    max_wall_minutes: 1\n  autonomy: strict\n"
	var legacy any
	if err := yaml.Unmarshal([]byte(legacyRole), &legacy); err != nil {
		t.Fatalf("unmarshal legacy role: %v", err)
	}
	jsonData, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy role: %v", err)
	}
	var value any
	if err := json.Unmarshal(jsonData, &value); err != nil {
		t.Fatalf("decode legacy role: %v", err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("legacy type fields unexpectedly rejected: %v", err)
	}

	mixedRole := "apiVersion: ai-sdlc-factory/v1\nkind: Role\nmetadata:\n  name: mixed-role\n  version: 1.0.0\n  description: Mixed role.\nspec:\n  system_prompt_ref: prompts/roles/mixed-role.md\n  inputs:\n    - name: request\n      type: document\n      artifact_type: document\n  outputs:\n    - name: result\n      artifact_type: document\n  model:\n    capability: code-fast\n  tools:\n    allow: []\n    deny: []\n  knowledge_scopes: [platform]\n  limits:\n    max_steps: 1\n    max_cost_usd: 0\n    max_wall_minutes: 1\n  autonomy: strict\n"
	var mixed any
	if err := yaml.Unmarshal([]byte(mixedRole), &mixed); err != nil {
		t.Fatalf("unmarshal mixed role: %v", err)
	}
	jsonData, err = json.Marshal(mixed)
	if err != nil {
		t.Fatalf("marshal mixed role: %v", err)
	}
	if err := json.Unmarshal(jsonData, &value); err != nil {
		t.Fatalf("decode mixed role: %v", err)
	}
	if err := schema.Validate(value); err == nil {
		t.Fatal("mixed type and artifact_type unexpectedly accepted")
	}
}

func TestRoleToolsFollowAntiInjectionSeparation(t *testing.T) {
	root := repositoryRoot(t)
	roleDirectory := filepath.Join(root, "catalog", "roles")
	schema := compileRoleSchema(t, filepath.Join(root, "packages", "contracts", "schemas", "role.schema.json"))
	if err := ValidateRoleDirectory(roleDirectory, schema); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(roleDirectory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(roleDirectory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var role roleDocument
		if err := yaml.Unmarshal(data, &role); err != nil {
			t.Fatalf("parse %s: %v", entry.Name(), err)
		}
		hasWeb, hasPrivateData, hasOutbound := false, false, false
		for _, tool := range role.Spec.Tools.Allow {
			hasWeb = hasWeb || strings.HasPrefix(tool, "web.")
			hasPrivateData = hasPrivateData || tool == "secrets.read" || strings.HasPrefix(tool, "fs.")
			hasOutbound = hasOutbound || strings.HasPrefix(tool, "git.push") ||
				strings.HasPrefix(tool, "deploy.") || tool == "tracker.write"
		}
		if hasWeb && hasPrivateData && hasOutbound {
			t.Errorf("%s combines web, private-data, and outbound tools", role.Metadata.Name)
		}
	}
}

func TestValidateRoleDirectoryRejectsInvalidCatalogs(t *testing.T) {
	schema := compileRoleSchema(t, filepath.Join(repositoryRoot(t), "packages", "contracts", "schemas", "role.schema.json"))
	validRole := "apiVersion: ai-sdlc-factory/v1\nkind: Role\nmetadata:\n  name: test-role\n  version: 1.0.0\n  description: Test role.\nspec:\n  system_prompt_ref: prompts/roles/test-role.md\n  inputs:\n    - name: request\n      artifact_type: document\n  outputs:\n    - name: result\n      artifact_type: document\n  model:\n    capability: code-fast\n  tools:\n    allow: []\n    deny: []\n  knowledge_scopes: [platform]\n  limits:\n    max_steps: 1\n    max_cost_usd: 0\n    max_wall_minutes: 1\n  autonomy: strict\n"
	validDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(validDirectory, "role.yaml"), []byte(validRole), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRoleDirectory(validDirectory, schema); err != nil {
		t.Fatalf("ValidateRoleDirectory(valid role) error = %v", err)
	}

	for _, test := range []struct {
		name        string
		content     string
		wantMessage string
	}{
		{name: "invalid YAML", content: "kind: [", wantMessage: "parse role"},
		{name: "invalid schema", content: strings.Replace(validRole, "ai-sdlc-factory/v1", "other/v1", 1), wantMessage: "validate role"},
		{name: "missing artifact_type", content: strings.Replace(validRole, "  inputs:\n    - name: request\n      artifact_type: document\n", "  inputs:\n    - name: request\n", 1), wantMessage: "validate role"},
		{name: "invalid artifact_type pattern", content: strings.Replace(validRole, "artifact_type: document", "artifact_type: Document", 1), wantMessage: "validate role"},
		{name: "multiple documents", content: validRole + "---\nkind: Role\n", wantMessage: "multiple YAML documents"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, "role.yaml"), []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			err := ValidateRoleDirectory(directory, schema)
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("ValidateRoleDirectory() error = %v, want message containing %q", err, test.wantMessage)
			}
		})
	}

	emptyDirectory := t.TempDir()
	if err := ValidateRoleDirectory(emptyDirectory, schema); err == nil || !strings.Contains(err.Error(), "contains no YAML files") {
		t.Fatalf("empty role directory error = %v", err)
	}
	if err := ValidateRoleDirectory(filepath.Join(t.TempDir(), "missing"), schema); err == nil || !strings.Contains(err.Error(), "read role catalog") {
		t.Fatalf("missing role directory error = %v", err)
	}
	if err := ValidateRoleDirectory(t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "role schema is required") {
		t.Fatalf("nil schema error = %v", err)
	}
}

// registryRoleSpecFixture returns a minimal registry role spec that satisfies
// every required field except tools_policy, which callers set explicitly.
func registryRoleSpecFixture() map[string]any {
	return map[string]any{
		"description":       "Test role.",
		"system_prompt_ref": "packages/ontology/prompts/test.yaml",
		"inputs":            []any{map[string]any{"name": "request", "artifact_type": "document"}},
		"outputs":           []any{map[string]any{"name": "result", "artifact_type": "document"}},
		"tools":             map[string]any{"allow": []any{"fs.read"}},
		"model":             map[string]any{"capability": "code-fast"},
		"limits":            map[string]any{"max_steps": 1, "max_cost_usd": 0},
		"autonomy":          "strict",
	}
}

// TestRegistryRoleSpecRequiresToolsPolicy guards the anti-injection contract
// (MCP_TOOLS.md §1, issue #118-a): a registry role spec MUST declare
// tools_policy, and the value MUST be one of read_only / write_capable.
// `mixed` is not a valid value by construction.
func TestRegistryRoleSpecRequiresToolsPolicy(t *testing.T) {
	schema := compileRoleSchema(t, filepath.Join(repositoryRoot(t), "packages", "contracts", "schemas", "role.schema.json"))

	if err := schema.Validate(registryRoleSpecFixture()); err == nil {
		t.Fatal("registry role spec without tools_policy unexpectedly accepted")
	}

	for _, invalid := range []string{"mixed", "", "write", "READ_ONLY"} {
		spec := registryRoleSpecFixture()
		spec["tools_policy"] = invalid
		if err := schema.Validate(spec); err == nil {
			t.Errorf("registry role spec with tools_policy=%q unexpectedly accepted", invalid)
		}
	}

	for _, valid := range []string{"read_only", "write_capable"} {
		spec := registryRoleSpecFixture()
		spec["tools_policy"] = valid
		if err := schema.Validate(spec); err != nil {
			t.Errorf("registry role spec with tools_policy=%q rejected: %v", valid, err)
		}
	}
}

// TestCatalogRolesDeclareToolsPolicy guards issue #118-a: every catalog role
// declares its anti-injection class. The field is optional in the schema for v1
// backward-compat, but the shipped catalog MUST declare it.
func TestCatalogRolesDeclareToolsPolicy(t *testing.T) {
	want := map[string]string{
		"backend-developer":  "write_capable",
		"frontend-developer": "write_capable",
		"code-reviewer":      "write_capable",
		"qa-engineer":        "write_capable",
		"product-manager":    "write_capable",
		"ux-ui-designer":     "write_capable",
		"business-analyst":   "read_only",
		"software-architect": "read_only",
		"product-researcher": "read_only",
	}
	for name, policy := range want {
		role := loadRole(t, name)
		if role.Spec.ToolsPolicy != policy {
			t.Errorf("%s tools_policy = %q, want %q", name, role.Spec.ToolsPolicy, policy)
		}
	}
}

func compileRoleSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	schema, err := jsonschema.Compile(path)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate role catalog test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "..", ".."))
}

func loadRole(t *testing.T, name string) roleDocument {
	t.Helper()
	root := repositoryRoot(t)
	path := filepath.Join(root, "catalog", "roles", name+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var role roleDocument
	if err := yaml.Unmarshal(data, &role); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return role
}

func requireTools(t *testing.T, role roleDocument, list string, tools ...string) {
	t.Helper()
	available := role.Spec.Tools.Allow
	if list == "deny" {
		available = role.Spec.Tools.Deny
	}
	for _, required := range tools {
		found := false
		for _, tool := range available {
			if tool == required {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s %s tools %v do not include %q", role.Metadata.Name, list, available, required)
		}
	}
}
