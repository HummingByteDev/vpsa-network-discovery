#!/usr/bin/env bash
# VAPN database restore from a backup.sh dump. DESTRUCTIVE: replaces the
# current database contents. The stack (except postgres) should be stopped.
#
# Usage: restore.sh /var/backups/vapn/vapn-20260718T031500Z.dump
set -euo pipefail

DUMP="${1:?usage: restore.sh <dump-file>}"
[[ -f "$DUMP" ]] || { echo "no such file: $DUMP" >&2; exit 1; }

cd "$(dirname "$0")/.."

echo "This will REPLACE the vapn database with $DUMP."
read -r -p "Type 'restore' to continue: " CONFIRM
[[ "$CONFIRM" == "restore" ]] || { echo "aborted"; exit 1; }

echo "stopping platform services (postgres stays up)..."
docker compose stop coordinator aggregator || true

# --clean --if-exists drops objects before recreating them; --no-owner keeps
# it portable across postgres users.
docker compose exec -T postgres pg_restore -U vapn -d vapn \
  --clean --if-exists --no-owner --exit-on-error < "$DUMP"

echo "restore ok; restarting services..."
docker compose up -d coordinator aggregator
echo "done. verify with: docker compose ps and vapnctl status"
