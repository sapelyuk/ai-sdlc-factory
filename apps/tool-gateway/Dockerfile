# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go mod files first for caching
COPY apps/tool-gateway/go.mod apps/tool-gateway/go.sum ./
RUN go mod download

# Copy source code
COPY apps/tool-gateway/ .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /tool-gateway ./cmd/tool-gateway/

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates \
    && addgroup -S appgroup \
    && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=builder /tool-gateway /app/tool-gateway
COPY packages/contracts/mcp-schemas /app/packages/contracts/mcp-schemas
COPY packages/policy /app/packages/policy

RUN chown -R appuser:appgroup /app

USER appuser

ENV PORT=8030 \
    SCHEMA_DIR=/app/packages/contracts/mcp-schemas \
    POLICY_DIR=/app/packages/policy

EXPOSE 8030

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8030/healthz || exit 1

CMD ["/app/tool-gateway"]
