package httptransport

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/domain"
	repo "github.com/sapelyuk/ai-sdlc-factory/apps/semantic/repository/postgres"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/service"
)

// Router wraps chi.Mux with common middleware and routes.
type Router struct {
	r *chi.Mux
}

// NewRouter creates a new router with all routes.
func NewRouter(
	nodeRepo *repo.NodeRepository,
	edgeRepo *repo.EdgeRepository,
	nodeSchema *jsonschema.Schema,
) *Router {
	nodeSvc := service.NewNodeService(nodeRepo)
	edgeSvc := service.NewEdgeService(edgeRepo)

	r := chi.NewMux()

	// Common middleware.
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type", "X-Workspace-ID", "X-Author-ID"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// Health endpoints (no auth).
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})

	// Tenant-context-protected semantic routes.
	r.Group(func(r chi.Router) {
		r.Use(TenantContextMiddleware)

		r.Post("/v1/nodes", createNodeHandler(nodeSvc, nodeSchema))
		r.Get("/v1/nodes", listNodesHandler(nodeSvc))
		r.Patch("/v1/nodes/{id}", updateNodeHandler(nodeSvc))
		r.Post("/v1/edges", createEdgeHandler(edgeSvc))
		r.Delete("/v1/edges/{id}", deleteEdgeHandler(edgeSvc))
	})

	return &Router{r: r}
}

// Handler returns the http.Handler.
func (r *Router) Handler() http.Handler {
	return r.r
}

// ── Node HTTP handlers ────────────────────────────────────────

func createNodeHandler(svc *service.NodeService, schema *jsonschema.Schema) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input domain.CreateNodeInput
		if err := readBody(r.Body, &input); err != nil {
			writeBodyError(w, err)
			return
		}
		data, err := json.Marshal(input)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode request")
			return
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to decode request")
			return
		}
		if schema != nil {
			if err := schema.Validate(value); err != nil {
				writeError(w, http.StatusUnprocessableEntity, validationMessage(err))
				return
			}
		}
		node, err := svc.Create(r.Context(), input)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, node)
	}
}

func listNodesHandler(svc *service.NodeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		layer := r.URL.Query().Get("layer")
		nodes, err := svc.List(r.Context(), layer)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, nodes)
	}
}

func updateNodeHandler(svc *service.NodeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		var input domain.UpdateNodeInput
		if err := readBody(r.Body, &input); err != nil {
			writeBodyError(w, err)
			return
		}
		node, err := svc.Update(r.Context(), id, input)
		if err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, node)
	}
}

// ── Edge HTTP handlers ────────────────────────────────────────

func createEdgeHandler(svc *service.EdgeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input domain.CreateEdgeInput
		if err := readBody(r.Body, &input); err != nil {
			writeBodyError(w, err)
			return
		}
		if err := svc.Create(r.Context(), input); err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
	}
}

func deleteEdgeHandler(svc *service.EdgeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		if err := svc.Delete(r.Context(), id); err != nil {
			writeRepositoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func validationMessage(err error) string {
	if validationErr, ok := err.(*jsonschema.ValidationError); ok {
		return validationErr.Error()
	}
	return err.Error()
}
