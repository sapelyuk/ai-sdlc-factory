package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

func ValidateRoleDirectory(directory string, schema *jsonschema.Schema) error {
	if schema == nil {
		return fmt.Errorf("role schema is required")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read role catalog %q: %w", directory, err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".yaml" {
			files = append(files, entry.Name())
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("role catalog %q contains no YAML files", directory)
	}

	for _, file := range files {
		path := filepath.Join(directory, file)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read role %q: %w", path, err)
		}
		var role any
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		if err := decoder.Decode(&role); err != nil {
			return fmt.Errorf("parse role %q: %w", path, err)
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			if err == nil {
				return fmt.Errorf("parse role %q: multiple YAML documents are not allowed", path)
			}
			return fmt.Errorf("parse role %q: %w", path, err)
		}

		jsonData, err := json.Marshal(role)
		if err != nil {
			return fmt.Errorf("encode role %q for validation: %w", path, err)
		}
		var value any
		if err := json.Unmarshal(jsonData, &value); err != nil {
			return fmt.Errorf("decode role %q for validation: %w", path, err)
		}
		if err := schema.Validate(value); err != nil {
			return fmt.Errorf("validate role %q: %w", path, err)
		}
	}
	return nil
}
