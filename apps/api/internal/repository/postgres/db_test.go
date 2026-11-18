package repository

import (
	"context"
	"testing"
)

func TestNewPoolCreatesPool(t *testing.T) {
	pool, err := NewPool(context.Background(), "postgres://test:test@127.0.0.1:5432/test?sslmode=disable")
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	if pool == nil {
		t.Fatal("NewPool() pool = nil, want non-nil pool")
	}
	pool.Close()
}

func TestNewPoolRejectsInvalidDSN(t *testing.T) {
	pool, err := NewPool(context.Background(), "://invalid")
	if err == nil {
		if pool != nil {
			pool.Close()
		}
		t.Fatal("NewPool() error = nil, want invalid DSN error")
	}
	if pool != nil {
		pool.Close()
		t.Fatal("NewPool() pool is non-nil, want nil pool")
	}
}
