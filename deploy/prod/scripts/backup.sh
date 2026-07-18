#!/usr/bin/env bash
# VAPN database backup: compressed pg_dump of everything that cannot be
# rebuilt (registry, measurements, aggregation, audit, scheduling, routing
# summaries). Artifacts in object storage are already redundant; routing
# snapshots can be rebuilt from RIS.
#
# Usage: backup.sh [output-dir]     (default /var/backups/vapn)
# Retention: VAPN_BACKUP_KEEP newest dumps are kept (default 14).
# Offsite: set VAPN_BACKUP_S3_URI (e.g. s3://bucket/vapn-backups) and have
# an S3 CLI (aws/mc/b2) configured; the dump is copied after writing.
set -euo pipefail

cd "$(dirname "$0")/.."   # deploy/prod — where docker-compose.yml lives

OUT_DIR="${1:-/var/backups/vapn}"
KEEP="${VAPN_BACKUP_KEEP:-14}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="$OUT_DIR/vapn-$STAMP.dump"

mkdir -p "$OUT_DIR"

# Custom format: compressed, supports parallel + selective restore.
docker compose exec -T postgres pg_dump -U vapn -d vapn --format=custom \
  > "$OUT.partial"
mv "$OUT.partial" "$OUT"

# Verify the dump is readable before trusting it.
docker compose exec -T postgres pg_restore --list < "$OUT" > /dev/null

SIZE=$(du -h "$OUT" | cut -f1)
echo "backup ok: $OUT ($SIZE)"

# Retention.
ls -1t "$OUT_DIR"/vapn-*.dump 2>/dev/null | tail -n +$((KEEP + 1)) | xargs -r rm -f

# Optional offsite copy.
if [[ -n "${VAPN_BACKUP_S3_URI:-}" ]]; then
  if command -v aws >/dev/null; then
    aws s3 cp "$OUT" "$VAPN_BACKUP_S3_URI/$(basename "$OUT")"
  elif command -v mc >/dev/null; then
    mc cp "$OUT" "$VAPN_BACKUP_S3_URI/$(basename "$OUT")"
  else
    echo "VAPN_BACKUP_S3_URI set but no aws/mc CLI found" >&2
    exit 1
  fi
  echo "offsite copy ok: $VAPN_BACKUP_S3_URI/$(basename "$OUT")"
fi
