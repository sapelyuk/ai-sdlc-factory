package main

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/config"
	mcpserver "github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/mcp"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/policy"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func setBaseEnv(t *testing.T, schemaDir, policyDir string) {
	t.Helper()
	t.Setenv("PORT", "8030")
	t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:1/db?sslmode=disable&connect_timeout=1")
	t.Setenv("OPA_URL", "embedded")
	t.Setenv("SCHEMA_DIR", schemaDir)
	t.Setenv("POLICY_DIR", policyDir)
	t.Setenv("VAULT_TOKEN", "")
}

func TestRunConfigError(t *testing.T) {
	setBaseEnv(t, "../../../../packages/contracts/mcp-schemas", "../../../../packages/policy")
	t.Setenv("PORT", "not-a-port")
	if err := run(discardLogger()); err == nil {
		t.Fatal("expected a config error")
	}
}

func TestRunSchemaError(t *testing.T) {
	setBaseEnv(t, t.TempDir(), "../../../../packages/policy")
	if err := run(discardLogger()); err == nil {
		t.Fatal("expected a schema loading error")
	}
}

func TestRunPolicyError(t *testing.T) {
	setBaseEnv(t, "../../../../packages/contracts/mcp-schemas", t.TempDir())
	if err := run(discardLogger()); err == nil {
		t.Fatal("expected a policy loading error")
	}
}

func TestRunDatabaseError(t *testing.T) {
	setBaseEnv(t, "../../../../packages/contracts/mcp-schemas", "../../../../packages/policy")
	if err := run(discardLogger()); err == nil {
		t.Fatal("expected a database connection error")
	}
}

func TestNewAuthorizer(t *testing.T) {
	logger := discardLogger()

	opaCfg := &config.Config{OPAURL: "http://localhost:8181"}
	engine, err := newAuthorizer(opaCfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := engine.(*policy.OPA); !ok {
		t.Fatalf("engine = %T, want *policy.OPA", engine)
	}

	embeddedCfg := &config.Config{
		PolicyDir: filepath.Join("..", "..", "..", "..", "packages", "policy"),
	}
	engine, err = newAuthorizer(embeddedCfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := engine.(*policy.Embedded); !ok {
		t.Fatalf("engine = %T, want *policy.Embedded", engine)
	}

	if _, err := newAuthorizer(&config.Config{PolicyDir: t.TempDir()}, logger); err == nil {
		t.Fatal("expected an error for an empty policy directory")
	}
}

func TestNewExecutor(t *testing.T) {
	logger := discardLogger()

	executor, err := newExecutor(&config.Config{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := executor.(mcpserver.StubExecutor); !ok {
		t.Fatalf("executor = %T, want mcpserver.StubExecutor", executor)
	}

	executor, err = newExecutor(&config.Config{MCPUpstreams: "*=http://sandbox:9000/mcp"}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := executor.(*mcpserver.ProxyExecutor); !ok {
		t.Fatalf("executor = %T, want *mcpserver.ProxyExecutor", executor)
	}

	if _, err := newExecutor(&config.Config{MCPUpstreams: "git.push"}, logger); err == nil {
		t.Fatal("expected an error for a malformed MCP_UPSTREAMS value")
	}
}
