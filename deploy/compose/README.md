# AI-SDLC Factory — локальное развёртывание

## Два профиля

| Профиль | Когда использовать | RAM | Langfuse |
|---|---|---|---|
| **local-dev** | Разработка и первые тесты (16 ГБ RAM) | ~10 ГБ | Выключен |
| **local-full** | Полноценная разработка (32+ ГБ RAM) | ~30 ГБ | Включён |

## Быстрый старт

```bash
# 1. Заполнить .env.local-dev (или .env.local-full)
cp deploy/compose/.env.example deploy/compose/.env.local-dev
$EDITOR deploy/compose/.env.local-dev

# 2. Поднять платформу
make up-local          # или make up-full

# 3. Проверить
make ps-local
```

## URL-ы сервисов

| Сервис | local-dev | local-full |
|---|---|---|
| Web UI | http://localhost:3000 | http://localhost:3000 |
| Platform API | http://localhost:8000 | http://localhost:8000 |
| Temporal UI | http://localhost:8080 | http://localhost:8080 |
| MinIO Console | http://localhost:9001 | http://localhost:9001 |
| Jaeger UI | http://localhost:16686 | http://localhost:16686 |
| Langfuse | — | http://localhost:3030 |
| Grafana | — | http://localhost:3001 |
| Prometheus | — | http://localhost:9090 |
| Vault | http://localhost:8200 | http://localhost:8200 |

## Остановка

```bash
make down-local        # или make down-full
```

## Полный сброс данных

```bash
make clean             # контейнеры и сети
make volumes           # ВСЕ тома (осторожно!)
```

## Если не хватает памяти (OOM)

1. Проверьте `docker stats` — какой контейнер ест больше всего.
2. Уменьшите лимиты в `docker-compose.local-dev.yml`.
3. Увеличьте swap на хосте.
4. Отключите ненужные сервисы в override-файле.
