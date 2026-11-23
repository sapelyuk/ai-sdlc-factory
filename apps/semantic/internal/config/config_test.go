package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		databaseURL string
		port        string
		seedID      string
		wantURL     string
		wantPort    int
		wantErr     bool
	}{
		{
			name:     "defaults",
			wantURL:  "postgres://postgres:postgres@localhost:5432/ai_sdlc_factory?sslmode=disable",
			wantPort: 8010,
		},
		{
			name:        "configured values",
			databaseURL: "postgres://db/semantic",
			port:        "9000",
			seedID:      "workspace-1",
			wantURL:     "postgres://db/semantic",
			wantPort:    9000,
		},
		{
			name:    "invalid port",
			port:    "not-a-port",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", tt.databaseURL)
			t.Setenv("PORT", tt.port)
			t.Setenv("SEED_WORKSPACE_ID", tt.seedID)

			got, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Load() error = nil, want invalid PORT error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got.DatabaseURL != tt.wantURL || got.Port != tt.wantPort || got.SeedWorkspaceID != tt.seedID {
				t.Fatalf("Load() = %#v, want URL %q, port %d, seed ID %q",
					got, tt.wantURL, tt.wantPort, tt.seedID)
			}
		})
	}
}

func TestFindSchemaDir(t *testing.T) {
	t.Run("uses configured directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "sem-node.schema.json"), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SCHEMA_DIR", dir)

		got, err := FindSchemaDir()
		if err != nil {
			t.Fatalf("FindSchemaDir() error = %v", err)
		}
		if got != dir {
			t.Fatalf("FindSchemaDir() = %q, want %q", got, dir)
		}
	})

	t.Run("falls back when configured directory is invalid", func(t *testing.T) {
		t.Setenv("SCHEMA_DIR", filepath.Join(t.TempDir(), "missing"))

		got, err := FindSchemaDir()
		if err != nil {
			t.Fatalf("FindSchemaDir() error = %v", err)
		}
		if _, err := os.Stat(filepath.Join(got, "sem-node.schema.json")); err != nil {
			t.Fatalf("fallback directory does not contain schema: %v", err)
		}
	})
}
