package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/domain"
	"gopkg.in/yaml.v3"
)

func TestSLPCoreSeedSpecsValidateAgainstJSONSchemas(t *testing.T) {
	slpPath, err := locateFile("", filepath.Join("packages", "ontology", "slp", "slp-core.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	schemaPath, err := locateFile("", filepath.Join("packages", "contracts", "schemas", "role.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(slpPath)
	if err != nil {
		t.Fatal(err)
	}
	var seed seedDocument
	if err := yaml.Unmarshal(data, &seed); err != nil {
		t.Fatal(err)
	}
	if len(seed.Roles) != 5 || len(seed.Teams) != 1 || len(seed.Processes) != 1 {
		t.Fatalf("SLP contents = %d roles, %d teams, %d processes; want 5, 1, 1",
			len(seed.Roles), len(seed.Teams), len(seed.Processes))
	}

	schemaDir := filepath.Dir(schemaPath)
	roleSchema := compileSchema(t, filepath.Join(schemaDir, "role.schema.json"))
	teamSchema := compileSchema(t, filepath.Join(schemaDir, "team.schema.json"))
	processSchema := compileSchema(t, filepath.Join(schemaDir, "process.schema.json"))
	for _, role := range seed.Roles {
		if err := validate(roleSchema, role.Spec); err != nil {
			t.Errorf("role %q schema validation failed: %v", role.Metadata.Name, err)
		}
	}
	for _, team := range seed.Teams {
		if err := validate(teamSchema, team.Spec); err != nil {
			t.Errorf("team %q schema validation failed: %v", team.Metadata.Name, err)
		}
	}
	for _, process := range seed.Processes {
		if err := validate(processSchema, process.Spec); err != nil {
			t.Errorf("process %q schema validation failed: %v", process.Metadata.Name, err)
		}
	}
}

func TestRegistrySchemasRejectInvalidEnums(t *testing.T) {
	schemaPath, err := locateFile("", filepath.Join("packages", "contracts", "schemas", "role.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	schemaDir := filepath.Dir(schemaPath)

	roleSchema := compileSchema(t, filepath.Join(schemaDir, "role.schema.json"))
	if err := validate(roleSchema, domain.RoleSpec{
		Description:     "Role",
		SystemPromptRef: "packages/ontology/prompts/role.yaml",
		Inputs:          []domain.ArtifactSpec{{Name: "request", Type: "document"}},
		Outputs:         []domain.ArtifactSpec{{Name: "result", Type: "document"}},
		Tools:           domain.ToolsSpec{Allow: []string{"fs.read"}},
		Model:           domain.ModelSpec{Capability: "unknown"},
		Limits:          domain.LimitsSpec{MaxSteps: 1, MaxCostUSD: 0},
		Autonomy:        domain.AutonomyBalanced,
	}); err == nil {
		t.Fatal("invalid model capability passed the role schema")
	}

	teamSchema := compileSchema(t, filepath.Join(schemaDir, "team.schema.json"))
	if err := validate(teamSchema, domain.TeamSpec{
		Description: "Team", Topology: "unknown",
		Members:      []domain.TeamMemberSpec{{RoleName: "qa", RoleVersion: "1.0.0", Count: 1}},
		Budget:       domain.BudgetSpec{MaxCostUSD: 1, MaxSteps: 1},
		ReviewMatrix: domain.ReviewMatrixSpec{RequiredReviewers: 1},
	}); err == nil {
		t.Fatal("invalid topology passed the team schema")
	}

	processSchema := compileSchema(t, filepath.Join(schemaDir, "process.schema.json"))
	if err := validate(processSchema, domain.ProcessSpec{
		Description: "Process",
		Stages:      []domain.StageSpec{{Name: "Idea", Order: 0}},
	}); err == nil {
		t.Fatal("process stage without a gate passed the process schema")
	}
	if err := validate(roleSchema, make(chan int)); err == nil {
		t.Fatal("validate accepted a value that cannot be JSON encoded")
	}
}

func TestSeedIDGeneratesUUIDv7AndValidatesConfiguredValue(t *testing.T) {
	t.Setenv("SEED_WORKSPACE_ID", "")
	generated, isGenerated, err := seedID("SEED_WORKSPACE_ID")
	if err != nil {
		t.Fatal(err)
	}

	if !isGenerated || generated.Version() != 7 {
		t.Fatalf("seedID() = %s, generated=%t, version=%d", generated, isGenerated, generated.Version())
	}

	configured := uuid.New()
	t.Setenv("SEED_WORKSPACE_ID", configured.String())
	got, isGenerated, err := seedID("SEED_WORKSPACE_ID")
	if err != nil {
		t.Fatal(err)
	}
	if isGenerated || got != configured {
		t.Fatalf("configured seed ID = %s, generated=%t", got, isGenerated)
	}

	t.Setenv("SEED_WORKSPACE_ID", "not-a-uuid")
	if _, _, err := seedID("SEED_WORKSPACE_ID"); err == nil {
		t.Fatal("invalid configured UUID was accepted")
	}
	t.Setenv("SEED_WORKSPACE_ID", uuid.Nil.String())
	if _, _, err := seedID("SEED_WORKSPACE_ID"); err == nil {
		t.Fatal("zero configured UUID was accepted")
	}
}

func TestSeedFileLookupVersionAndEarlyRunErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "seed.yaml")
	if err := os.WriteFile(file, []byte("kind: SLP\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	located, err := locateFile(file, "unused")
	if err != nil || located != file {
		t.Fatalf("locateFile(configured) = %q, %v; want %q", located, err, file)
	}
	if _, err := locateFile(filepath.Join(t.TempDir(), "missing.yaml"), "unused"); err == nil {
		t.Fatal("locateFile accepted a missing configured file")
	}
	if got := seedVersion(""); got != "1.0.0" {
		t.Fatalf("seedVersion(empty) = %q, want 1.0.0", got)
	}
	if got := seedVersion("2.3.4"); got != "2.3.4" {
		t.Fatalf("seedVersion(2.3.4) = %q", got)
	}

	t.Setenv("SEED_WORKSPACE_ID", "invalid")
	if err := run(context.Background()); err == nil {
		t.Fatal("run accepted an invalid workspace UUID")
	}

	t.Setenv("SEED_WORKSPACE_ID", uuid.NewString())
	t.Setenv("SEED_AUTHOR_ID", uuid.NewString())
	t.Setenv("SLP_PATH", file)
	if err := run(context.Background()); err == nil || !strings.Contains(err.Error(), "kind SLP") {
		t.Fatalf("run with invalid SLP header error = %v; want kind SLP error", err)
	}
	if err := os.WriteFile(file, []byte("kind: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background()); err == nil || !strings.Contains(err.Error(), "parse SLP YAML") {
		t.Fatalf("run with malformed SLP YAML error = %v; want YAML parse error", err)
	}
	if err := os.WriteFile(file, []byte("apiVersion: ai-sdlc-factory/v1\nkind: SLP\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PORT", "invalid")
	if err := run(context.Background()); err == nil || !strings.Contains(err.Error(), "PORT:") {
		t.Fatalf("run with invalid PORT error = %v; want configuration error", err)
	}
	t.Setenv("PORT", "")
	t.Setenv("DATABASE_URL", "://")
	if err := run(context.Background()); err == nil || !strings.Contains(err.Error(), "create database pool") {
		t.Fatalf("run with invalid database URL error = %v; want pool configuration error", err)
	}
	if _, err := locateFile("", filepath.Join("missing", uuid.NewString(), "slp.yaml")); err == nil {
		t.Fatal("locateFile unexpectedly found a missing relative file")
	}
}

func compileSchema(t *testing.T, path string) *jsonschema.Schema {
	t.Helper()
	schema, err := jsonschema.Compile(path)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}
