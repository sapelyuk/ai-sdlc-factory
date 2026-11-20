package testutil

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPoolInterfaceContract verifies that *pgxpool.Pool satisfies the
// postgresPool interface at compile time. This is a pure contract test —
// it does not start Docker, it only ensures the interface methods match.
func TestPoolInterfaceContract(t *testing.T) {
	// Compile-time interface satisfaction check.
	// If postgresPool signature changes and *pgxpool.Pool no longer matches,
	// this line will fail to compile.
	var _ postgresPool = (*pgxpool.Pool)(nil)
}

// TestContainerInterfaceContract verifies that the postgresContainer interface
// is well-formed. No runtime Docker call — compile-time only.
func TestContainerInterfaceContract(t *testing.T) {
	// postgresContainer is implemented by postgrescontainer.Container from
	// testcontainers-go. We only verify the interface type exists and is usable.
	var _ = (postgresContainer)(nil)
}

// TestRuntimeFieldTypes verifies that postgresRuntime field types are correct.
// This prevents accidental breakage of the struct fields used by StartPostgres.
func TestRuntimeFieldTypes(t *testing.T) {
	r := postgresRuntime{
		run:      func(ctx context.Context, name string) (postgresContainer, error) { return nil, nil },
		newPool:  func(ctx context.Context, dsn string) (postgresPool, error) { return nil, nil },
		readFile: func(string) ([]byte, error) { return nil, nil },
		repoRoot: func(*testing.T) string { return "" },
	}
	// Verify the struct is constructible with the expected field types.
	_ = r
}
