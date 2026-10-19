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
