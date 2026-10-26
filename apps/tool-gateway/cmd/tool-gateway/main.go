// Command tool-gateway implements the ai-sdlc-factory Tool Gateway (issue #10).
//
// The service is an MCP proxy built on github.com/mark3labs/mcp-go (ADR-022).
// It exposes Streamable HTTP on /mcp, enforces OPA policies (R0-R3, ADR-013),
// writes a hash-chained audit log and brokers credentials from Vault.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpsdk "github.com/mark3labs/mcp-go/server"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/audit"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/catalog"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/config"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/credentials"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/identity"
	mcpserver "github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/mcp"
	"github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/policy"
	httptransport "github.com/sapelyuk/ai-sdlc-factory/apps/tool-gateway/internal/transport/http"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("tool-gateway failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Fail fast on a malformed schema file: an agent must never see a
	// partially loaded tool catalog.
	cat, err := catalog.Load(cfg.SchemaDir)
	if err != nil {
		return err
	}
	cat.LogSummary(logger)

	authorizer, err := newAuthorizer(cfg, logger)
	if err != nil {
		return err
	}

	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		return err
	}

	auditor := audit.NewLogger(audit.NewPostgresStore(pool))

	var broker credentials.Broker
	if cfg.VaultToken != "" {
		vaultBroker, err := credentials.NewVaultBroker(cfg.VaultAddr, cfg.VaultToken, cfg.VaultCacheTTL)
		if err != nil {
			return err
		}
		broker = vaultBroker
		logger.Info("credential brokering enabled", "vault", cfg.VaultAddr)
	} else {
		logger.Warn("VAULT_TOKEN is empty; credential brokering is disabled")
	}

	redactor := credentials.NewRedactor()
	gateway, err := mcpserver.NewServer(mcpserver.Options{
		Catalog:    cat,
		Authorizer: authorizer,
		Auditor:    auditor,
		Broker:     broker,
		Redactor:   redactor,
		Logger:     logger,
	})
	if err != nil {
		return err
	}

	streamable := mcpsdk.NewStreamableHTTPServer(
		gateway.MCP(),
		mcpsdk.WithEndpointPath("/mcp"),
		mcpsdk.WithStateLess(true),
		mcpsdk.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			id, err := identity.FromHeaders(r.Header)
			if err != nil {
				return ctx
			}
			return identity.With(ctx, id)
		}),
	)

	router := httptransport.NewRouter(httptransport.Options{
		MCP:        streamable,
		Catalog:    cat,
		Authorizer: authorizer,
		Auditor:    auditor,
		DB:         pool,
		Logger:     logger,
	})

	addr := ":" + strconv.Itoa(cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           router.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	go func() {
		logger.Info("ai-sdlc-factory-tool-gateway listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return err
	}
	logger.Info("stopped")
	return nil
}

func newAuthorizer(cfg *config.Config, logger *slog.Logger) (policy.Authorizer, error) {
	if cfg.OPAURL != "" {
		engine, err := policy.NewOPA(cfg.OPAURL, cfg.PolicyTimeout)
		if err != nil {
			return nil, err
		}
		logger.Info("using OPA HTTP policy engine", "url", cfg.OPAURL)
		return engine, nil
	}

	engine, err := policy.NewEmbedded(cfg.PolicyDir)
	if err != nil {
		return nil, err
	}
	logger.Info("using in-process policy engine", "dir", cfg.PolicyDir)
	return engine, nil
}
