FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY apps/sandbox-manager/go.mod apps/sandbox-manager/go.sum ./
RUN go mod download

COPY apps/sandbox-manager/ .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /sandboxmanager ./cmd/

FROM alpine:3.20

RUN apk add --no-cache ca-certificates \
    && addgroup -S appgroup \
    && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=builder /sandboxmanager /app/sandboxmanager

RUN chown -R appuser:appgroup /app

USER appuser

ENV PORT=8060

EXPOSE 8060

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8060/healthz || exit 1

CMD ["/app/sandboxmanager"]
