// Package main is the entry point for the Semantic Service.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/internal/config"
	repo "github.com/sapelyuk/ai-sdlc-factory/apps/semantic/repository/postgres"
	httptransport "github.com/sapelyuk/ai-sdlc-factory/apps/semantic/transport/http"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	level := slog.LevelInfo
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	conn, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer conn.Close()

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := conn.Ping(pingCtx); err != nil {
		pingCancel()
		slog.Error("failed to ping database", "error", err)
		os.Exit(1)
	}
	pingCancel()
	slog.Info("connected to database")

	// Compile JSON schemas for validation.
	schemaDir, err := config.FindSchemaDir()
	if err != nil {
		slog.Error("failed to find schema directory", "error", err)
		os.Exit(1)
	}
	nodeSchema, err := jsonschema.Compile(schemaDir + "/sem-node.schema.json")
	if err != nil {
		slog.Error("failed to compile node schema", "error", err)
		os.Exit(1)
	}

	nodeRepo := repo.NewNodeRepository(conn)
	edgeRepo := repo.NewEdgeRepository(conn)

	r := httptransport.NewRouter(nodeRepo, edgeRepo, nodeSchema)

	srv := &http.Server{
		Addr:         ":" + strconv.Itoa(cfg.Port),
		Handler:      r.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("starting semantic server", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-quit
	slog.Info("shutting down semantic server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}

	slog.Info("semantic server exited")
}
