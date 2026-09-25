#!/usr/bin/env bash
# Delete uploads older than RETENTION_DAYS. The public demo accepts files from
# strangers; keeping them forever is a liability nobody asked for.
set -euo pipefail

UPLOAD_DIR="${UPLOAD_DIR:-/var/lib/profx/uploads}"
RETENTION_DAYS="${RETENTION_DAYS:-7}"

if [ ! -d "$UPLOAD_DIR" ]; then
  echo "upload directory $UPLOAD_DIR does not exist"
  exit 0
fi

before=$(find "$UPLOAD_DIR" -type f | wc -l)
find "$UPLOAD_DIR" -type f -mtime "+${RETENTION_DAYS}" -delete
after=$(find "$UPLOAD_DIR" -type f | wc -l)

echo "uploads: ${before} -> ${after} (deleted $((before - after)) older than ${RETENTION_DAYS} days)"
