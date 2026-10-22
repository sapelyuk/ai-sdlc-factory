#!/usr/bin/env bash
# Apply or roll back database migrations via docker compose exec.
# Reads COMPOSE_FILES, MIGRATIONS_DIR, DB_SERVICE from the environment
# (exported by the Makefile). Windows counterpart: scripts/migrate.ps1.
#
# Usage: migrate.sh up|down
#
# Kept compatible with bash 3.2 (the macOS system bash): no mapfile, no arrays.
set -uo pipefail

DIRECTION="${1:?Usage: migrate.sh up|down}"

COMPOSE_CMD="docker compose ${COMPOSE_FILES:?COMPOSE_FILES must be exported by Makefile}"
MIGRATIONS_DIR="${MIGRATIONS_DIR:-migrations/postgres}"
DB_SERVICE="${DB_SERVICE:-postgres}"

case "$DIRECTION" in
    up)   suffix=".up.sql"   verb="Applying"     sort_order=""   done_msg="==> All migrations applied" ;;
    down) suffix=".down.sql" verb="Rolling back" sort_order="-r" done_msg="==> All migrations rolled back" ;;
    *)    echo "Unknown direction: $DIRECTION (expected up|down)" >&2; exit 1 ;;
esac

# `|| true` keeps a non-matching glob from tripping pipefail, so the empty
# check below can report it as an explicit error instead of failing silently.
files=$(ls "$MIGRATIONS_DIR"/*"$suffix" 2>/dev/null | sort $sort_order || true)

if [ -z "$files" ]; then
    echo "No migration files found for $MIGRATIONS_DIR/*$suffix" >&2
    exit 1
fi

# SQL is piped via stdin because the migration files live on the host, not
# inside the postgres container. Credentials are read from the standard
# POSTGRES_USER/POSTGRES_DB variables that Compose sets in the container.
for f in $files; do
    echo "==> $verb $f"
    $COMPOSE_CMD exec -T "$DB_SERVICE" sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"' < "$f" || exit 1
done

echo "$done_msg"
