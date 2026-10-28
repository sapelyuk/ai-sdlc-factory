package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/sandbox-manager/internal/sandbox"
)

const (
	defaultTimeoutSeconds = 300
	defaultOutputLimit    = 1024 * 1024
	maxRequestBytes       = 64 * 1024
)

// ErrServerClosed aliases http.ErrServerClosed for the command's shutdown path.
var ErrServerClosed = http.ErrServerClosed

// Options configures the sandbox HTTP router.
type Options struct {
	Manager *sandbox.Manager
	Tasks   sandbox.TaskRunStore
	Logger  *slog.Logger
}

// NewRouter registers health and sandbox execution endpoints.
func NewRouter(opts Options) *http.ServeMux {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := opts.Tasks.Ready(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		if err := opts.Manager.Ready(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "docker proxy unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.Handle("POST /v1/sandboxes/exec", tenantMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		execute(w, r, opts)
	})))
	return mux
}

// NewServer creates the HTTP server with standard timeout protections.
func NewServer(port int, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

type tenant struct {
	workspaceID uuid.UUID
	authorID    uuid.UUID
}

type tenantContextKey struct{}

func tenantMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID, err := parseHeaderUUID(r, "X-Workspace-ID")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		authorID, err := parseHeaderUUID(r, "X-Author-ID")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		ctx := context.WithValue(r.Context(), tenantContextKey{}, tenant{
			workspaceID: workspaceID,
			authorID:    authorID,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type execRequest struct {
	TaskRunID        string   `json:"task_run_id"`
	Command          []string `json:"command"`
	TimeoutSeconds   int      `json:"timeout_seconds"`
	StdoutLimitBytes int      `json:"stdout_limit_bytes"`
}

type execResponse struct {
	Status          string `json:"status"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	ExitCode        int    `json:"exit_code"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
}

func execute(w http.ResponseWriter, r *http.Request, opts Options) {
	var request execRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request body must contain a single JSON object")
		return
	}
	taskRunID, err := uuid.Parse(request.TaskRunID)
	if err != nil || taskRunID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "task_run_id must be a valid UUID")
		return
	}
	if len(request.Command) == 0 || strings.TrimSpace(request.Command[0]) == "" {
		writeError(w, http.StatusBadRequest, "command must contain at least one non-empty argument")
		return
	}
	for _, arg := range request.Command {
		if strings.IndexByte(arg, 0) >= 0 {
			writeError(w, http.StatusBadRequest, "command arguments must not contain NUL bytes")
			return
		}
	}
	if request.TimeoutSeconds == 0 {
		request.TimeoutSeconds = defaultTimeoutSeconds
	}
	if request.TimeoutSeconds < 1 || request.TimeoutSeconds > 3600 {
		writeError(w, http.StatusBadRequest, "timeout_seconds must be between 1 and 3600")
		return
	}
	if request.StdoutLimitBytes == 0 {
		request.StdoutLimitBytes = defaultOutputLimit
	}
	if request.StdoutLimitBytes < 1 || request.StdoutLimitBytes > 16*1024*1024 {
		writeError(w, http.StatusBadRequest, "stdout_limit_bytes must be between 1 and 16777216")
		return
	}
	currentTenant, ok := r.Context().Value(tenantContextKey{}).(tenant)
	if !ok {
		writeError(w, http.StatusBadRequest, "tenant context is required")
		return
	}

	workspacePath, err := opts.Tasks.WorkspacePath(r.Context(), taskRunID, currentTenant.workspaceID)
	if errors.Is(err, sandbox.ErrTaskRunNotFound) {
		writeError(w, http.StatusForbidden, "task_run does not belong to X-Workspace-ID")
		return
	}
	if err != nil {
		opts.Logger.Error("resolve task_run workspace", "error", err, "task_run_id", taskRunID)
		writeError(w, http.StatusInternalServerError, "failed to resolve task_run workspace")
		return
	}
	if strings.TrimSpace(workspacePath) == "" {
		writeError(w, http.StatusBadRequest, "task_run has no workspace_path; run migrate-up and ensure Agent Runtime sets it")
		return
	}

	containerID, err := opts.Manager.Create(r.Context(), currentTenant.workspaceID, taskRunID, workspacePath)
	if err != nil {
		opts.Logger.Error("create sandbox", "error", err, "task_run_id", taskRunID)
		writeError(w, http.StatusInternalServerError, "failed to create sandbox")
		return
	}
	stdout, stderr, exitCode, stdoutTruncated, stderrTruncated, execErr := opts.Manager.Exec(
		r.Context(),
		containerID,
		request.Command,
		time.Duration(request.TimeoutSeconds)*time.Second,
		request.StdoutLimitBytes,
	)
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()
	destroyReason := "exec_completed"
	if execErr != nil {
		destroyReason = "exec_failed"
	} else if exitCode != 0 {
		destroyReason = "exec_nonzero_exit"
	}
	destroyErr := opts.Manager.Destroy(cleanupCtx, currentTenant.workspaceID, containerID, destroyReason)
	if execErr != nil {
		opts.Logger.Error("execute sandbox command", "error", execErr, "task_run_id", taskRunID)
		if destroyErr != nil {
			opts.Logger.Error("destroy sandbox after execution error", "error", destroyErr, "task_run_id", taskRunID)
		}
		if errors.Is(execErr, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "sandbox command timed out")
			return
		}
		writeError(w, http.StatusInternalServerError, "sandbox command failed")
		return
	}
	if destroyErr != nil {
		opts.Logger.Error("destroy sandbox", "error", destroyErr, "task_run_id", taskRunID)
		writeError(w, http.StatusInternalServerError, "failed to destroy sandbox")
		return
	}

	status := "success"
	if exitCode != 0 {
		status = "error"
	}
	writeJSON(w, http.StatusOK, execResponse{
		Status:          status,
		Stdout:          stdout,
		Stderr:          stderr,
		ExitCode:        exitCode,
		StdoutTruncated: stdoutTruncated,
		StderrTruncated: stderrTruncated,
	})
}

func parseHeaderUUID(r *http.Request, name string) (uuid.UUID, error) {
	raw := r.Header.Get(name)
	if raw == "" {
		return uuid.Nil, fmt.Errorf("%s header is required", name)
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%s must be a valid UUID", name)
	}
	return id, nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write HTTP response", "error", err)
	}
}
