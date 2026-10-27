FROM golang:1.26-alpine

RUN apk add --no-cache \
      bash \
      build-base \
      ca-certificates \
      git \
      nodejs \
      npm \
      python3 \
      py3-pip \
    && addgroup -g 1000 sandbox \
    && adduser -D -u 1000 -G sandbox sandbox \
    && mkdir -p /workspace \
    && chown 1000:1000 /workspace

WORKDIR /workspace
USER 1000:1000

CMD ["sleep", "infinity"]
