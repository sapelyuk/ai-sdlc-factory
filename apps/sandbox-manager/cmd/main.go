// Command sandboxmanager runs the ai-sdlc-factory sandbox manager.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/docker/docker/client"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/sandbox-manager/internal/config"
	"github.com/sapelyuk/ai-sdlc-factory/apps/sandbox-manager/internal/sandbox"
	httptransport "github.com/sapelyuk/ai-sdlc-factory/apps/sandbox-manager/internal/transport/http"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger); err != nil {
		logger.Error("sandbox-manager failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	dockerClient, err := client.NewClientWithOpts(
		client.WithHost(cfg.DockerHost),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return err
	}
	defer func() {
		if err := dockerClient.Close(); err != nil {
			logger.Error("close Docker client", "error", err)
		}
	}()

	dbPool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer dbPool.Close()
	if err := dbPool.Ping(context.Background()); err != nil {
		return err
	}

	manager := sandbox.NewManager(dockerClient, cfg, logger, sdkaudit.NewLogger(dbPool))
	router := httptransport.NewRouter(httptransport.Options{
		Manager: manager,
		Tasks:   sandbox.NewPostgresTaskRunStore(dbPool),
		Logger:  logger,
	})
	server := httptransport.NewServer(cfg.Port, router)
	go func() {
		logger.Info("ai-sdlc-factory-sandbox-manager listening", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, httptransport.ErrServerClosed) {
			logger.Error("http server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}
