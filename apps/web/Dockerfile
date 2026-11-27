# Build stage
#
# The build context is the repository root (see deploy/compose/docker-compose.yml),
# not apps/web: src/lib/sdlc.ts imports
# ../../../../packages/contracts/state-machine/sdlc_transitions.json, so the
# layout inside the image must mirror the repository. Keeping a single copy of
# the contract in packages/contracts avoids a second source of truth.
FROM node:20-alpine AS builder

WORKDIR /src

# Copy package files first for caching
COPY apps/web/package*.json ./apps/web/

# Install dependencies
RUN cd apps/web && npm ci

# Copy source code and the contract it imports
COPY apps/web ./apps/web
COPY packages/contracts/state-machine/sdlc_transitions.json ./packages/contracts/state-machine/sdlc_transitions.json

# Build the application
RUN cd apps/web && npm run build

# Production stage
FROM nginx:alpine

# Copy built assets
COPY --from=builder /src/apps/web/dist /usr/share/nginx/html

# Nginx configuration
RUN cat > /etc/nginx/conf.d/default.conf <<'EOF'
server {
    listen 3000;
    server_name _;

    root /usr/share/nginx/html;
    index index.html;

    # Security headers
    add_header X-Frame-Options "SAMEORIGIN" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-XSS-Protection "1; mode=block" always;

    # SPA fallback — route all requests to index.html
    location / {
        try_files $uri $uri/ /index.html;
    }

    # API proxy — forward API requests to backend
    location /api/ {
        proxy_pass http://api:8000;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # Gzip compression
    gzip on;
    gzip_types text/plain text/css application/json application/javascript text/xml application/xml text/javascript;
    gzip_min_length 1000;
}
EOF

EXPOSE 3000

CMD ["nginx", "-g", "daemon off;"]
