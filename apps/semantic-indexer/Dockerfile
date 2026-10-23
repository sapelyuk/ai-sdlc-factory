# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go mod files first for caching
COPY apps/semantic-indexer/go.mod ./
RUN go mod download

# Copy source code
COPY apps/semantic-indexer/ .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o /semanticindexer ./cmd/

# Runtime stage
FROM alpine:3.20

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=builder /semanticindexer /app/semanticindexer

RUN chown -R appuser:appgroup /app

USER appuser

EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8000/healthz || exit 1

CMD ["/app/semanticindexer"]
