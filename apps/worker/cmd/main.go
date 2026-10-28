// Command worker starts the ai-sdlc-factory Temporal worker.
//
// It registers TaskRunWorkflow, SDLCWorkflow, and AgentStepActivity on the
// ai-sdlc-factory task queue. See issue #104.
package main

import (
	"log"
	"net/http"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/activity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/agent"
	httpproto "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/config"
	agentctx "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/context"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
	wf "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/workflow"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
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
