#!/bin/sh
# Entrypoint script for Subscriptions-API service

set -e

# Use direct PostgreSQL URL for migrate/seed to bypass PgBouncer transaction mode.
MIGRATE_URL="${POSTGRES_MIGRATE_URL:-$POSTGRES_URL}"

echo "========================================="
echo "Subscriptions-API Service Startup"
echo "========================================="

echo "Waiting for database and running migrations..."
MAX_RETRIES=60
RETRY_COUNT=0

# Liveness kills this container (connection refused on :4000) well before MAX_RETRIES*5s
# elapses, so the real failure must be visible on EVERY attempt, not just a final summary --
# a summary that never gets logged is as good as /dev/null. A permanent failure (e.g. a
# migration pre-flight guard refusing to run against dirty data) looks identical to this loop
# as a transient one, and printing only "not ready" for either hid that distinction for hours.
until MIGRATE_OUTPUT=$(POSTGRES_URL="$MIGRATE_URL" /usr/local/bin/subscription-migrate 2>&1) || [ $RETRY_COUNT -eq $MAX_RETRIES ]; do
  RETRY_COUNT=$((RETRY_COUNT+1))
  echo "Migration attempt $RETRY_COUNT/$MAX_RETRIES failed:"
  echo "$MIGRATE_OUTPUT"
  sleep 5
done

if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
  echo "Migration failed after $MAX_RETRIES attempts. Last error:"
  echo "$MIGRATE_OUTPUT"
  exit 1
fi

echo "Migrations applied successfully"

echo ""
echo "========================================="
echo "Running seed (idempotent)"
echo "========================================="
POSTGRES_URL="$MIGRATE_URL" /usr/local/bin/subscription-seed || echo "Seed completed with warnings (non-fatal)"

echo ""
echo "========================================="
echo "Starting Subscriptions-API server"
echo "========================================="
echo ""

exec /usr/local/bin/subscription-api
