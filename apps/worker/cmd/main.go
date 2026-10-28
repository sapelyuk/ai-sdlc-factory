// Command worker starts the ai-sdlc-factory Temporal worker.
//
// It registers TaskRunWorkflow, SDLCWorkflow, and AgentStepActivity on the
// ai-sdlc-factory task queue. See issue #104.
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	temporalactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/activity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/agent"
	httpproto "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/config"
	agentctx "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/context"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
	wf "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/workflow"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Connect to PostgreSQL for the global audit log (issue #14).
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		log.Fatalf("database ping: %v", err)
	}

	// Connect to Temporal server.
	tClient, err := client.Dial(client.Options{
		HostPort: cfg.TemporalHost,
	})
	if err != nil {
		log.Fatalf("temporal client: %v", err)
	}
	defer tClient.Close()

	// Build HTTP clients.
	llmClient := httpproto.NewLLMClient(cfg.LLMGatewayURL)
	toolClient := httpproto.NewToolClient(cfg.ToolGatewayURL)
	semClient := httpproto.NewSemanticClient(cfg.SemanticURL)
	kbClient := httpproto.NewKnowledgeClient(cfg.KnowledgeURL)

	// Build context builder and handoff components.
	ctxBuilder := agentctx.NewBuilder(semClient, kbClient)
	hb := handoff.NewBuilder()
	hv, err := handoff.NewValidator()
	if err != nil {
		log.Fatalf("handoff validator: %v", err)
	}

	// Build agent executor.
	executor := agent.NewExecutor(llmClient, toolClient, ctxBuilder, hb, hv)

	// Create Temporal worker.
	w := worker.New(tClient, cfg.TaskQueue, worker.Options{})

	// Register workflows.
	w.RegisterWorkflow(wf.TaskRunWorkflow)
	w.RegisterWorkflow(wf.SDLCWorkflow)

	// Register activities.
	stepActivity := activity.NewAgentStepActivity(executor)
	w.RegisterActivity(stepActivity.Execute)
	// Every audit-worthy step of a run is appended to the global tamper-evident
	// audit log through this activity (issue #14).
	auditActivity := activity.NewAuditActivity(sdkaudit.NewLogger(pool))
	w.RegisterActivityWithOptions(auditActivity.Execute, temporalactivity.RegisterOptions{
		Name: wf.AuditRecordActivityName,
	})

	// Start health-check server in background.
	go startHealthz()

	log.Printf("ai-sdlc-factory-worker starting on task queue %q", cfg.TaskQueue)
	if err := w.Start(); err != nil {
		log.Fatalf("worker start: %v", err)
	}

	// Block forever.
	select {}
}

// startHealthz serves GET /healthz for Docker Compose health checks.
func startHealthz() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if err := http.ListenAndServe(":8000", mux); err != nil {
		log.Printf("healthz server: %v", err)
	}
}
