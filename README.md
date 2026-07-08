# Subscription Service

The Subscription Service provides centralized subscription and licensing management for all Codevertex properties, enabling multi-tenant SaaS operations with tiered pricing, feature gating, usage tracking, and seamless plan transitions.

## Current Status

🚧 **In Development** - Sprint 0 (Foundation) in progress

## Quick Start

```shell
cp config/example.env .env
make deps
docker compose up -d postgres redis nats
go generate ./internal/ent
go run ./cmd/api
```

Default HTTP port: `4005` (`SUBSCRIPTION_HTTP_PORT` override).

## Architecture

- **Language**: Go 1.24+
- **Database**: PostgreSQL 16+ with Ent ORM
- **Cache**: Redis 7+
- **Events**: NATS JetStream
- **Auth**: JWT via `shared-auth-client` library

See `docs/plan.md` for detailed implementation plan.
