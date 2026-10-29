# Build stage
FROM golang:1.26-alpine AS builder

# The app resolves the shared SDK through a `replace` directive that points at
# ../../packages/sdk-go, so the build tree has to keep that relative layout.
WORKDIR /app/apps/worker

# Copy go mod files first for caching
COPY apps/worker/go.mod apps/worker/go.sum ./
COPY packages/sdk-go/go.mod packages/sdk-go/go.sum /app/packages/sdk-go/
RUN go mod download

# Copy source code
COPY apps/worker/ ./
COPY packages/sdk-go/ /app/packages/sdk-go/

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o /worker ./cmd/

# Runtime stage
FROM alpine:3.20

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=builder /worker /app/worker

RUN chown -R appuser:appgroup /app

USER appuser

# Worker does not expose HTTP ports — healthz is internal only.

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8000/healthz || exit 1

CMD ["/app/worker"]
