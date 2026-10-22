// Package config holds test helpers.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// FindSchemaDir locates the contracts schemas directory.
func FindSchemaDir() (string, error) {
	var candidates []string
	if configured := os.Getenv("SCHEMA_DIR"); configured != "" {
		candidates = append(candidates, configured)
	}
	candidates = append(candidates, filepath.Join("packages", "contracts", "schemas"))
	if _, sourceFile, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "..", "..", "packages", "contracts", "schemas"))
	}
	for _, candidate := range candidates {
		dir, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "sem-node.schema.json")); err == nil {
			return dir, nil
		}
	}
	// Fallback: walk up from cwd.
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		dir, _ = filepath.Abs(dir)
		if _, err := os.Stat(filepath.Join(dir, "packages", "contracts", "schemas", "sem-node.schema.json")); err == nil {
			return filepath.Join(dir, "packages", "contracts", "schemas"), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("could not find semantic JSON-Schemas; set SCHEMA_DIR")
}
