// Package main is the entry point for the API server.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/config"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/iam"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/handler"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/repository"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/telemetry"
	httptransport "github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/transport/http"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	if err := cfg.ValidateAPIAuth(); err != nil {
		slog.Error("invalid API authentication configuration", "error", err)
		os.Exit(1)
	}

	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	// Tracing must be initialized before the HTTP server starts so that all
	// requests are recorded by the global tracer provider.
	shutdownTracer, err := telemetry.InitTracer(context.Background(), "api-server")
	if err != nil {
		slog.Error("failed to initialize tracing", "error", err)
		os.Exit(1)
	}
	var shutdownOnce sync.Once
	shutdownTracing := func() {
		shutdownOnce.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := shutdownTracer(ctx); err != nil {
				slog.Error("failed to shutdown tracer", "error", err)
			}
		})
	}
	defer shutdownTracing()

	conn, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer conn.Close()

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := conn.Ping(pingCtx); err != nil {
		pingCancel()
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	pingCancel()
	slog.Info("connected to database")

	roleRepo := repository.NewPostgresRoleRepository(conn)
	teamRepo := repository.NewPostgresTeamRepository(conn)
	processRepo := repository.NewPostgresProcessRepository(conn)

	registryHandler, err := handler.NewRegistryHandler(roleRepo, teamRepo, processRepo)
	if err != nil {
		slog.Error("failed to initialize registry handler", "error", err)
		os.Exit(1)
	}

	// IAM setup.
	userRepo := iam.NewPostgresUserRepository(conn)
	jwtService := iam.NewJWTService(cfg.JWTSecret, cfg.JWTDuration)
	loginHandler := httptransport.NewLoginHandler(userRepo, jwtService)

	r := httptransport.NewRouter(registryHandler, conn.Ping, cfg, jwtService, loginHandler)

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
		slog.Info("starting server", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-quit
	slog.Info("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced to shutdown", "error", err)
		os.Exit(1)
	}

	shutdownTracing()

	slog.Info("server exited")
}
