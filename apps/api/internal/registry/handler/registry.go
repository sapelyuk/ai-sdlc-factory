package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/repository"
)

const maxRequestBytes = 1 << 20

var errRequestTooLarge = errors.New("request body exceeds 1 MiB")

type RegistryHandler struct {
	roles         repository.RoleRepository
	teams         repository.TeamRepository
	processes     repository.ProcessRepository
	roleSchema    *jsonschema.Schema
	teamSchema    *jsonschema.Schema
	processSchema *jsonschema.Schema
}

func NewRegistryHandler(
	roles repository.RoleRepository,
	teams repository.TeamRepository,
	processes repository.ProcessRepository,
) (*RegistryHandler, error) {
	schemaDir, err := findSchemaDirectory()
	if err != nil {
		return nil, err
	}
	roleSchema, err := jsonschema.Compile(filepath.Join(schemaDir, "role.schema.json"))
	if err != nil {
		return nil, fmt.Errorf("compile role schema: %w", err)
	}
	teamSchema, err := jsonschema.Compile(filepath.Join(schemaDir, "team.schema.json"))
	if err != nil {
		return nil, fmt.Errorf("compile team schema: %w", err)
	}
	processSchema, err := jsonschema.Compile(filepath.Join(schemaDir, "process.schema.json"))
	if err != nil {
		return nil, fmt.Errorf("compile process schema: %w", err)
	}
	return &RegistryHandler{
		roles: roles, teams: teams, processes: processes,
		roleSchema: roleSchema, teamSchema: teamSchema, processSchema: processSchema,
	}, nil
}

func findSchemaDirectory() (string, error) {
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
		if info, err := os.Stat(filepath.Join(dir, "role.schema.json")); err == nil && !info.IsDir() {
			return dir, nil
		}
	}
	return "", fmt.Errorf("could not find Registry JSON-Schemas; set SCHEMA_DIR")
}

func validateSpec(schema *jsonschema.Schema, spec any) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("encode spec for validation: %w", err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode spec for validation: %w", err)
	}
	if err := schema.Validate(value); err != nil {
		return err
	}
	return nil
}

func decodeJSON(r io.Reader, target any) error {
	data, err := io.ReadAll(io.LimitReader(r, maxRequestBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxRequestBytes {
		return errRequestTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("request body must contain a single JSON value")
		}
		return err
	}
	return nil
}

func writeBodyError(w http.ResponseWriter, err error) {
	if errors.Is(err, errRequestTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, "invalid JSON request body: "+err.Error())
}

func validationMessage(err error) string {
	if validationErr, ok := err.(*jsonschema.ValidationError); ok {
		return strings.TrimSpace(validationErr.Error())
	}
	return err.Error()
}

func checkEnvelope(apiVersion, kind, expectedKind string) error {
	if apiVersion != "" && apiVersion != "ai-sdlc-factory/v1" {
		return fmt.Errorf("apiVersion must be ai-sdlc-factory/v1")
	}
	if kind != "" && kind != expectedKind {
		return fmt.Errorf("kind must be %s", expectedKind)
	}
	return nil
}

func validName(name string) bool {
	length := utf8.RuneCountInString(name)
	return length > 0 && length <= 128
}

func requestBody(r io.Reader, target any) error {
	return decodeJSON(r, target)
}

func parseID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("failed to encode Registry response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeRepositoryError(w http.ResponseWriter, err error) {
	status, message := registryErrorStatus(err)
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			status, message = http.StatusConflict, "a registry version with this name already exists"
		case "23514":
			status, message = http.StatusUnprocessableEntity, "registry data violates a database constraint"
		}
	}
	if status == http.StatusInternalServerError {
		slog.Error("Registry repository operation failed", "error", err)
	}
	writeError(w, status, message)
}

func registryErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "registry item not found"
	case errors.Is(err, domain.ErrImmutable):
		return http.StatusMethodNotAllowed, err.Error()
	case errors.Is(err, domain.ErrInvalidStatusTransition):
		return http.StatusConflict, err.Error()
	case errors.Is(err, domain.ErrApproverIsAuthor):
		return http.StatusForbidden, err.Error()
	default:
		if errors.Is(err, identity.ErrMissingTenantContext) {
			return http.StatusBadRequest, err.Error()
		}
		return http.StatusInternalServerError, "internal registry error"
	}
}
