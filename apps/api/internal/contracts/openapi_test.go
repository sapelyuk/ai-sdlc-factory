package contracts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRegistryOpenAPIContract(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate OpenAPI contract test source")
	}
	repositoryRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "..")
	specPath := filepath.Join(repositoryRoot, "packages", "contracts", "openapi", "registry.yaml")
	specData, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read Registry OpenAPI contract: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(specData, &document); err != nil {
		t.Fatalf("parse Registry OpenAPI contract: %v", err)
	}
	if got, _ := document["openapi"].(string); got != "3.1.0" {
		t.Fatalf("OpenAPI version = %q, want 3.1.0", got)
	}
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatal("Registry OpenAPI contract has no paths object")
	}
	if got, want := len(paths), 18; got != want {
		t.Fatalf("documented Registry paths = %d, want %d", got, want)
	}
	for path, value := range paths {
		pathItem, ok := value.(map[string]any)
		if !ok {
			t.Errorf("path %q is not an object", path)
			continue
		}
		for method, operation := range pathItem {
			if method == "parameters" {
				continue
			}
			operationObject, ok := operation.(map[string]any)
			if !ok {
				t.Errorf("operation %s %s is not an object", method, path)
				continue
			}
			if operationID, ok := operationObject["operationId"].(string); !ok || operationID == "" {
				t.Errorf("operation %s %s has no operationId", method, path)
			}
		}
	}
	if err := validateExternalSchemaRefs(document, filepath.Dir(specPath)); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformAPIOpenAPIContract(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate OpenAPI contract test source")
	}
	repositoryRoot := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "..")
	specPath := filepath.Join(repositoryRoot, "packages", "contracts", "openapi", "platform-api.yaml")
	specData, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read Platform API OpenAPI contract: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(specData, &document); err != nil {
		t.Fatalf("parse Platform API OpenAPI contract: %v", err)
	}
	if got, _ := document["openapi"].(string); got != "3.1.0" {
		t.Fatalf("OpenAPI version = %q, want 3.1.0", got)
	}
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatal("Platform API OpenAPI contract has no paths object")
	}

	// Verify all expected paths are present.
	expectedPaths := []string{
		"/api/v1/auth/login",
		"/healthz",
		"/readyz",
		"/metrics",
		"/api/v1/registry/roles",
		"/api/v1/registry/roles/{id}",
		"/api/v1/registry/teams",
		"/api/v1/registry/processes",
	}
	for _, p := range expectedPaths {
		if _, ok := paths[p]; !ok {
			t.Errorf("expected path %q not found in Platform API spec", p)
		}
	}

	// Verify security scheme.
	components, _ := document["components"].(map[string]any)
	securitySchemes, _ := components["securitySchemes"].(map[string]any)
	bearer, ok := securitySchemes["bearerAuth"].(map[string]any)
	if !ok {
		t.Fatal("bearerAuth security scheme not found")
	}
	if got, _ := bearer["scheme"].(string); got != "bearer" {
		t.Errorf("bearerAuth scheme = %q, want bearer", got)
	}
	if got, _ := bearer["bearerFormat"].(string); got != "JWT" {
		t.Errorf("bearerAuth bearerFormat = %q, want JWT", got)
	}

	// Verify login endpoint.
	loginPath, ok := paths["/api/v1/auth/login"].(map[string]any)
	if !ok {
		t.Fatal("/api/v1/auth/login path not found")
	}
	if _, ok := loginPath["post"]; !ok {
		t.Error("POST /api/v1/auth/login not found")
	}

	for path, value := range paths {
		pathItem, ok := value.(map[string]any)
		if !ok {
			t.Errorf("path %q is not an object", path)
			continue
		}
		for method, operation := range pathItem {
			if method == "parameters" {
				continue
			}
			operationObject, ok := operation.(map[string]any)
			if !ok {
				t.Errorf("operation %s %s is not an object", method, path)
				continue
			}
			if operationID, ok := operationObject["operationId"].(string); !ok || operationID == "" {
				t.Errorf("operation %s %s has no operationId", method, path)
			}
		}
	}
	if err := validateExternalSchemaRefs(document, filepath.Dir(specPath)); err != nil {
		t.Fatal(err)
	}
}

func validateExternalSchemaRefs(value any, openAPIDirectory string) error {
	switch current := value.(type) {
	case map[string]any:
		if ref, ok := current["$ref"].(string); ok && filepath.Ext(ref) == ".json" {
			schemaPath := filepath.Clean(filepath.Join(openAPIDirectory, filepath.FromSlash(ref)))
			schemaData, err := os.ReadFile(schemaPath)
			if err != nil {
				return err
			}
			var schema map[string]any
			if err := json.Unmarshal(schemaData, &schema); err != nil {
				return err
			}
			if schema["$schema"] == nil {
				return fmt.Errorf("external JSON Schema has no $schema dialect: %s", schemaPath)
			}
		}
		for _, child := range current {
			if err := validateExternalSchemaRefs(child, openAPIDirectory); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range current {
			if err := validateExternalSchemaRefs(child, openAPIDirectory); err != nil {
				return err
			}
		}
	}
	return nil
}
