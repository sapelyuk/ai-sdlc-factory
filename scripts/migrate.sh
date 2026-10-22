#!/usr/bin/env bash
# Apply or rollback database migrations via docker compose exec.
# Reads COMPOSE_FILES, MIGRATIONS_DIR, DB_SERVICE from environment (exported by Makefile).
# Usage: migrate.sh up|down
set -euo pipefail

DIRECTION="${1:?Usage: migrate.sh up|down}"

COMPOSE_CMD="docker compose ${COMPOSE_FILES:?COMPOSE_FILES must be exported by Makefile}"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-migrations/postgres}"
DB_SERVICE="${DB_SERVICE:-postgres}"

if [ "$DIRECTION" = "up" ]; then
    mapfile -t files < <(ls "$MIGRATIONS_DIR"/*.up.sql 2>/dev/null | sort)
    if [ "${#files[@]}" -eq 0 ]; then
        echo "No migration files found for $MIGRATIONS_DIR/*.up.sql" >&2
        exit 1
    fi
    for f in "${files[@]}"; do
        echo "==> Applying $f"
        $COMPOSE_CMD exec -T "$DB_SERVICE" sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < "$f" || exit 1
    done
    echo "==> All migrations applied"
elif [ "$DIRECTION" = "down" ]; then
    mapfile -t files < <(ls "$MIGRATIONS_DIR"/*.down.sql 2>/dev/null | sort -r)
    if [ "${#files[@]}" -eq 0 ]; then
        echo "No migration files found for $MIGRATIONS_DIR/*.down.sql" >&2
        exit 1
    fi
    for f in "${files[@]}"; do
        echo "==> Rolling back $f"
        $COMPOSE_CMD exec -T "$DB_SERVICE" sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < "$f" || exit 1
    done
    echo "==> All migrations rolled back"
else
    echo "Unknown direction: $DIRECTION (expected up|down)" >&2
    exit 1
fi
