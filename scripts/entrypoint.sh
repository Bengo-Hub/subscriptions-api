#!/bin/sh
# Entrypoint script for Subscriptions-API service
# Runs migrations (via app startup) and seed before starting the server

set -e

echo "========================================="
echo " Subscriptions-API - Starting"
echo "========================================="

echo "Running seed (idempotent)..."
/usr/local/bin/seed || echo "Seed completed with warnings (non-fatal)"

echo ""
echo "========================================="
echo " Subscriptions-API - Seeding complete"
echo "========================================="
echo ""

exec /usr/local/bin/subscription-api
