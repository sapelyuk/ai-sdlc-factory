package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const schemasDir = "../../../../packages/contracts/mcp-schemas"

func TestLoadRealSchemas(t *testing.T) {
	c, err := Load(schemasDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Files() == 0 {
		t.Fatal("expected at least one schema file")
	}
	if c.Len() == 0 {
		t.Fatal("expected at least one tool")
	}

	for _, tool := range c.Tools() {
		if tool.Name == "" || tool.Description == "" {
			t.Errorf("tool is missing name or description: %+v", tool)
		}
		if tool.RiskLevel != "R0" && tool.RiskLevel != "R1" &&
			tool.RiskLevel != "R2" && tool.RiskLevel != "R3" {
			t.Errorf("tool %s has invalid risk level %q", tool.Name, tool.RiskLevel)
		}
	}

	push, ok := c.Tool("git.push")
	if !ok {
		t.Fatal("git.push not loaded")
	}
	if push.RiskLevel != "R1" {
		t.Errorf("git.push risk = %s, want R1", push.RiskLevel)
	}

	// Short role names used by agents resolve to schema role names.
	devTools := c.ToolNamesForRole("developer")
	if len(devTools) == 0 {
		t.Fatal("developer role has no tools")
	}
	if !contains(devTools, "git.push") {
		t.Error("developer role should include git.push")
	}
	if !contains(c.DenyForRole("developer"), "deploy.prod") {
		t.Error("developer deny list should include deploy.prod")
	}

	qaTools := c.ToolNamesForRole("qa-engineer")
	if !contains(qaTools, "test.run") {
		t.Error("qa-engineer role should include test.run")
	}
}

func TestLoadRejectsBrokenSchemas(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{
			name: "invalid json",
			file: `{"role": "x",`,
			want: "parse JSON",
		},
		{
			name: "missing role",
			file: `{"schema_version": "ai-sdlc-factory/v1", "tools": []}`,
			want: "role is required",
		},
		{
			name: "wrong version",
			file: `{"role":"x","schema_version":"v0","tools":[` + validTool + `]}`,
			want: "schema_version",
		},
		{
			name: "no tools",
			file: `{"role":"x","schema_version":"ai-sdlc-factory/v1","tools":[]}`,
			want: "tools must not be empty",
		},
		{
			name: "invalid risk",
			file: `{"role":"x","schema_version":"ai-sdlc-factory/v1","tools":[` +
				strings.Replace(validTool, `"R1"`, `"R9"`, 1) + `]}`,
			want: "risk_level",
		},
		{
			name: "missing description",
			file: `{"role":"x","schema_version":"ai-sdlc-factory/v1","tools":[` +
				strings.Replace(validTool, `"description":"d",`, ``, 1) + `]}`,
			want: "description is required",
		},
		{
			name: "schema is not an object",
			file: `{"role":"x","schema_version":"ai-sdlc-factory/v1","tools":[` +
				strings.Replace(validTool, `"type":"object"`, `"type":"array"`, 1) + `]}`,
			want: "input_schema.type",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "role.json"), []byte(tc.file), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(dir)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestLoadEmptyDir(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("expected an error for an empty schema directory")
	}
	if _, err := Load(""); err == nil {
		t.Fatal("expected an error for an empty directory path")
	}
}

func TestLoadRejectsConflictingRiskLevels(t *testing.T) {
	dir := t.TempDir()
	write := func(name, role, risk string) {
		content := `{"role":"` + role + `","schema_version":"ai-sdlc-factory/v1","tools":[` +
			strings.Replace(validTool, `"R1"`, `"`+risk+`"`, 1) + `]}`
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.json", "role-a", "R1")
	write("b.json", "role-b", "R2")

	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "conflicting risk levels") {
		t.Fatalf("expected conflicting risk levels error, got %v", err)
	}
}

func TestLoadRejectsDuplicateToolInRole(t *testing.T) {
	dir := t.TempDir()
	content := `{"role":"x","schema_version":"ai-sdlc-factory/v1","tools":[` +
		validTool + `,` + validTool + `]}`
	if err := os.WriteFile(filepath.Join(dir, "role.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "duplicate tool") {
		t.Fatalf("expected duplicate tool error, got %v", err)
	}
}

func TestRolesSorted(t *testing.T) {
	c, err := Load(schemasDir)
	if err != nil {
		t.Fatal(err)
	}
	roles := c.Roles()
	for i := 1; i < len(roles); i++ {
		if roles[i-1] > roles[i] {
			t.Fatalf("roles are not sorted: %v", roles)
		}
	}
}

const validTool = `{"name":"git.push","description":"d","input_schema":{"type":"object","properties":{}},"risk_level":"R1"}`

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
