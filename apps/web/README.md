# Web UI — ai-sdlc-factory

Web UI scaffold for the ai-sdlc-factory platform.

## Stack

- **Vite 6** + **React 18** + **TypeScript** (strict)
- **Tailwind CSS 4** + **shadcn/ui** (Button, Input, Table, Card)
- **TanStack Query** (data fetching)
- **Zustand** (state management, auth)
- **React Router DOM** (routing)
- **Vitest** (unit tests)

## Styling

Tailwind CSS 4 uses CSS-first configuration: theme tokens are declared via
`@theme inline` in `src/index.css`, PostCSS loads `@tailwindcss/postcss`.
Tailwind 4 requires Safari 16.4+, Chrome 111+ or Firefox 128+.

The JWT is kept in the in-memory auth store and is cleared on page reload. It is
not persisted to `localStorage`.

## Quick start

```bash
# 1. Navigate to the directory
cd apps/web

# 2. Install dependencies
npm ci

# 3. Start the dev server
npm run dev
# → http://localhost:5173

# 4. Build the production bundle
npm run build

# 5. Run tests
npm run test
```

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `VITE_API_URL` | `/api` | API endpoint prefix (for Docker: `/api`, for dev: Vite proxy) |

## Structure

```
apps/web/
├── src/
│   ├── components/
│   │   ├── ui/          # shadcn/ui components
│   │   ├── AuthProvider.tsx
│   │   └── ProtectedRoute.tsx
│   ├── hooks/
│   │   └── use-toast.tsx
│   ├── lib/
│   │   ├── api.ts       # API client (login, fetchRoles)
│   │   ├── auth-context.tsx  # Auth context
│   │   ├── sse.ts       # SSE client (stub)
│   │   └── utils.ts     # cn() utility
│   ├── pages/
│   │   ├── Login.tsx    # Login page
│   │   ├── Dashboard.tsx # Dashboard
│   │   ├── RegistryRoles.tsx # Registry Roles (real API)
│   │   └── StubPage.tsx  # Stub for Pages
│   ├── store/
│   │   └── auth.ts      # Zustand auth store
│   ├── App.tsx          # Routing
│   ├── main.tsx         # Entry point
│   └── index.css        # Tailwind
├── Dockerfile           # Production build (nginx)
├── vite.config.ts       # Vite config (proxy → API)
├── vitest.config.ts     # Vitest config
└── package.json
```

## Docker

```bash
# Build and run
docker build -t ai-sdlc-factory/web:local .
docker run -p 3000:3000 ai-sdlc-factory/web:local
```

In the compose infrastructure:
```bash
make up-local
# → http://localhost:3000
```

## Tests

```bash
npm run test      # Run all tests
npm run test:watch # Watch mode
```

## Known limitations

- Teams and Processes pages are stubs (to be implemented in subsequent tasks).
- E2E tests (Playwright) — optional, may be deferred.
- SSE client is a stub, not connected to the real API.
