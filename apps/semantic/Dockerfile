# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Copy go mod files first for caching
COPY apps/semantic/go.mod apps/semantic/go.sum ./
RUN go mod download

# Copy source code
COPY apps/semantic/ .
COPY packages/contracts/schemas /app/packages/contracts/schemas
COPY packages/ontology/slp /app/packages/ontology/slp

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o /semantic-server ./cmd/semantic-server/
RUN CGO_ENABLED=0 GOOS=linux go build -o /seed-slp ./cmd/seed-slp/

# Runtime stage
FROM alpine:3.20

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=builder /semantic-server /app/semantic-server
COPY --from=builder /seed-slp /app/seed-slp
COPY packages/contracts/schemas /app/packages/contracts/schemas
COPY packages/ontology/slp /app/packages/ontology/slp

RUN chown -R appuser:appgroup /app

USER appuser

EXPOSE 8010

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8010/healthz || exit 1

CMD ["/app/semantic-server"]
