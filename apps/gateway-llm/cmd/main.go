// Command gatewayllm implements the ai-sdlc-factory LLM Gateway per ADR-021.
//
// It exposes:
//   - GET  /healthz  — liveness probe
//   - GET  /readyz   — readiness probe (pings PostgreSQL)
//   - POST /v1/llm/chat  — profile-based LLM routing with fallback and cost attribution
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/catalog"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/config"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/providers"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/repository"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/routing"
	httptransport "github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/transport/http"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Connect to PostgreSQL.
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		log.Fatalf("database ping: %v", err)
	}

	// Load the global model catalog.
	cat := catalog.New(pool)
	if err := cat.Load(context.Background()); err != nil {
		log.Fatalf("catalog load: %v", err)
	}

	// Build provider map.
	prov := make(map[string]providers.Provider)
	if cfg.AnthropicAPIKey != "" {
		prov["anthropic"] = providers.NewAnthropic(cfg.AnthropicAPIKey)
	}
	if cfg.OpenAIAPIKey != "" {
		prov["openai"] = providers.NewOpenAI(cfg.OpenAIAPIKey)
	}
	if cfg.OllamaBaseURL != "" {
		prov["ollama"] = providers.NewOllama(cfg.OllamaBaseURL)
	}
	log.Printf("providers registered: %v", listKeys(prov))

	// Wire routing, audit and HTTP.
	repo := repository.New(pool)
	rtr := routing.New(cat, repo, prov)
	// Every finished logical call is appended to the global tamper-evident
	// audit log (issue #14).
	rtr.WithAuditor(sdkaudit.NewLogger(pool))
	httpRouter := httptransport.NewRouter(rtr, pool)

	// Start server.
	addr := ":" + strconv.Itoa(cfg.Port)
	srv := &http.Server{Addr: addr, Handler: httpRouter.Handler()}

	go func() {
		log.Printf("ai-sdlc-factory-gateway-llm listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	// Graceful shutdown on SIGTERM / SIGINT.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("stopped")
}

func listKeys(m map[string]providers.Provider) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
