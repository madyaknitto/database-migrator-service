#!/usr/bin/env bash
set -euo pipefail

# Helper untuk mengambil dump schema existing PostgreSQL sebagai baseline awal.
# Penggunaan:
#   POSTGRES_HOST=... POSTGRES_PORT=5432 POSTGRES_USER=... POSTGRES_PASSWORD=... ./scripts/dump-baseline.sh <db_name> [actual_database]

if [ "$#" -lt 1 ]; then
  echo "Penggunaan: $0 <db_name> [actual_database]"
  exit 1
fi

DB_NAME="$1"
ACTUAL_DB="${2:-$DB_NAME}"
HOST="${POSTGRES_HOST:-${PG_HOST:-${DB_HOST:-localhost}}}"
PORT="${POSTGRES_PORT:-${PG_PORT:-${DB_PORT:-5432}}}"
USER="${POSTGRES_USER:-${PG_USER:-${DB_USER:-migrator_user}}}"
PASS="${POSTGRES_PASSWORD:-${PGPASSWORD:-${DB_PASSWORD:-}}}"

OUT_DIR="databases/${DB_NAME}/baseline"
OUT_FILE="${OUT_DIR}/00000000000000_initial_baseline.sql"
mkdir -p "$OUT_DIR"

echo "Mengambil schema baseline PostgreSQL dari ${HOST}:${PORT}/${ACTUAL_DB} ke ${OUT_FILE}..."
PGPASSWORD="$PASS" pg_dump \
  --host="$HOST" \
  --port="$PORT" \
  --username="$USER" \
  --dbname="$ACTUAL_DB" \
  --schema-only \
  --no-owner \
  --no-privileges \
  --no-comments \
  --file="$OUT_FILE"

echo "Selesai! Periksa isi ${OUT_FILE} sebelum commit."
