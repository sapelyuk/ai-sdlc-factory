# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go mod files first for caching
COPY apps/gateway-llm/go.mod apps/gateway-llm/go.sum ./
RUN go mod download

# Copy source code
COPY apps/gateway-llm/ .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o /gatewayllm ./cmd/

# Runtime stage
FROM alpine:3.20

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=builder /gatewayllm /app/gatewayllm

RUN chown -R appuser:appgroup /app

USER appuser

EXPOSE 8040

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8040/healthz || exit 1

CMD ["/app/gatewayllm"]
