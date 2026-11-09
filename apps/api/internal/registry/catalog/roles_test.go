package catalog

import (
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
		Autonomy string `yaml:"autonomy"`
	} `yaml:"spec"`
}

type artifactDocument struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"`
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
	validRole := "apiVersion: ai-sdlc-factory/v1\nkind: Role\nmetadata:\n  name: test-role\n  version: 1.0.0\n  description: Test role.\nspec:\n  system_prompt_ref: prompts/roles/test-role.md\n  inputs:\n    - name: request\n      type: document\n  outputs:\n    - name: result\n      type: document\n  model:\n    capability: code-fast\n  tools:\n    allow: []\n    deny: []\n  knowledge_scopes: [platform]\n  limits:\n    max_steps: 1\n    max_cost_usd: 0\n    max_wall_minutes: 1\n  autonomy: strict\n"
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
