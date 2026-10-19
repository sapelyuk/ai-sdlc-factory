# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Copy go mod files first for caching
COPY apps/api/go.mod apps/api/go.sum ./
RUN go mod download

# Copy source code
COPY apps/api/ .
COPY packages/contracts/schemas /app/packages/contracts/schemas

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o /api-server ./cmd/api-server/

# Runtime stage
FROM alpine:3.20

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=builder /api-server /app/api-server

RUN chown -R appuser:appgroup /app

USER appuser

EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/healthz || exit 1

CMD ["/app/api-server"]
