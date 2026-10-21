# Web UI — ai-sdlc-factory

Каркас Web UI платформы ai-sdlc-factory.

## Стек

- **Vite 6** + **React 18** + **TypeScript** (strict)
- **Tailwind CSS 4** + **shadcn/ui** (Button, Input, Table, Card)
- **TanStack Query** (data fetching)
- **Zustand** (state management, auth)
- **React Router DOM** (routing)
- **Vitest** (unit tests)

## Стилизация

Tailwind CSS 4 использует CSS-first конфигурацию: токены темы объявлены через
`@theme inline` в `src/index.css`, PostCSS подключает `@tailwindcss/postcss`.
Tailwind 4 требует Safari 16.4+, Chrome 111+ или Firefox 128+.

The JWT is kept in the in-memory auth store and is cleared on page reload. It is
not persisted to `localStorage`.

## Быстрый старт

```bash
# 1. Перейти в каталог
cd apps/web

# 2. Установить зависимости
npm ci

# 3. Запустить dev-сервер
npm run dev
# → http://localhost:5173

# 4. Собрать production-бандл
npm run build

# 5. Запустить тесты
npm run test
```

## Переменные окружения

| Переменная | Значение по умолчанию | Описание |
|---|---|---|
| `VITE_API_URL` | `/api` | Префикс API-эндпоинтов (для Docker — `/api`, для dev — прокси Vite) |

## Структура

```
apps/web/
├── src/
│   ├── components/
│   │   ├── ui/          # shadcn/ui компоненты
│   │   ├── AuthProvider.tsx
│   │   └── ProtectedRoute.tsx
│   ├── hooks/
│   │   └── use-toast.tsx
│   ├── lib/
│   │   ├── api.ts       # API-клиент (login, fetchRoles)
│   │   ├── auth-context.tsx  # Auth context
│   │   ├── sse.ts       # SSE client (заглушка)
│   │   └── utils.ts     # cn() утилита
│   ├── pages/
│   │   ├── Login.tsx    # Страница логина
│   │   ├── Dashboard.tsx # Dashboard
│   │   ├── RegistryRoles.tsx # Registry Roles (реальный API)
│   │   └── StubPage.tsx  # Заглушка для Pages
│   ├── store/
│   │   └── auth.ts      # Zustand auth store
│   ├── App.tsx          # Роутинг
│   ├── main.tsx         # Точка входа
│   └── index.css        # Tailwind
├── Dockerfile           # Production-сборка (nginx)
├── vite.config.ts       # Vite-конфиг (proxy → API)
├── vitest.config.ts     # Vitest-конфиг
└── package.json
```

## Docker

```bash
# Собрать и запустить
docker build -t ai-sdlc-factory/web:local .
docker run -p 3000:3000 ai-sdlc-factory/web:local
```

В compose-инфраструктуре:
```bash
make up-local
# → http://localhost:3000
```

## Тесты

```bash
npm run test      # Запустить все тесты
npm run test:watch # Watch-режим
```

## Известные ограничения

- Страницы Teams и Processes — заглушки (будут в следующих задачах).
- E2E-тесты (Playwright) — опционально, могут быть отложены.
- SSE-клиент — заглушка, не подключена к реальному API.
