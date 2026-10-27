package main

import (
	"io"
	"log/slog"
	"testing"
)

func TestRunRequiresDockerProxy(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	if err := run(slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("run() should fail when DOCKER_HOST is missing")
	}
}

func TestRunReturnsDatabaseConnectionError(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2375")
	t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:1/db?connect_timeout=1")
	if err := run(slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("run() should fail when PostgreSQL is unreachable")
	}
}
