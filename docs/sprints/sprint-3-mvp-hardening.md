# Sprint 3: MVP Hardening

**Sprint**: 3
**Deadline**: March 27, 2026
**Status**: ✅ P0 Mostly Done — Feature gate endpoint + Redis caching + tenant.created consumer + usage tracking implemented. Atlas migration + JWT E2E verification pending.
**Goal**: Production-ready feature gating, JWT claims enrichment, event-driven provisioning, Atlas migration transition

**Progress (March 2026)**: MVP launch critical-path services (ordering, cafe-website, auth, notifications) updated; subscription-api remains dependency for Trinity. Tick items as implemented. **RBAC/Redis/Events:** No local RBAC; identity and roles from auth-api GET /me. Subscriptions-ui uses TanStack Query (useMe, 5 min TTL) for /me; nav and route protection use roles/permissions; `/unauthorized` page added. Redis and NATS/outbox events documented in plan.md. **Seed & RBAC audit (March 2026):** cmd/seed seeds products, plans, bundles, and demo tenant subscription (Urban Loft → GROWTH trial). No local Permission/Role schema; RBAC via auth-api JWT. Subscription resources (plans, subscriptions, features) use the eight actions (add, read, read_own, change, change_own, delete, manage, manage_own) in auth-api.

---

## Context

Sprints 1-2 delivered the database schema, seed data, CRUD endpoints, state machine, and outbox pattern. Sprint 3 hardens the service for the BengoBox MVP launch (March 27). The subscription-api is a critical-path dependency — every other service relies on it for Trinity Authorization (RBAC + Licensing + Resources).

---

## Deliverables

### P0 — Must Ship

#### 1. Redis Feature-Gate Caching ✅ DONE
- [x] `GET /api/v1/features/{code}/check` — `FeatureHandler.CheckFeature` with cache-aside
- [x] Cache key: `subscription:feature:{tenant_id}:{feature_code}` with 60s TTL
- [x] `GET /api/v1/features` — full entitlements endpoint with cache key `subscription:entitlements:{tenant_id}`
- [x] `FeatureHandler.InvalidateCache(ctx, tenantID)` — purges entitlements + all feature keys via pattern
- [x] Wire `InvalidateCache` calls on subscription mutations (create, change plan, cancel, renew) ✅ DONE (May 2026)
- [x] Redis health already in `/healthz` endpoint

#### 2. JWT Claims Enrichment — End-to-End Verification
- [ ] Verify `GET /tenants/{tenant_id}/subscription` response format matches auth-service expectations
- [ ] Ensure `features[]` and `limits{}` are populated correctly for all 6 plans
- [ ] Test with `urban-loft` tenant: confirm GROWTH features/limits in JWT
- [ ] Add integration test: create subscription → verify response → simulate auth-service call

#### 3. `auth.tenant.created` Event Consumer ✅ DONE
- [x] `internal/modules/consumers/tenant_created.go` — JetStream durable consumer
- [x] On event: auto-creates Starter + delivery bundle with 14-day trial
- [x] Idempotency: CreateSubscription returns gracefully on duplicate
- [x] Durable consumer name: `subscription-service-tenant-provisioner`, MaxDeliver 5
- [x] Wired in `app.go` `Run()` alongside outbox publisher
- [ ] Test with manual NATS publish — pending smoke test

#### 4. Atlas Migration Transition
- [ ] Install Atlas CLI in Dockerfile and CI
- [ ] Generate initial baseline migration from current Ent schema
- [ ] Replace `ormClient.Schema.Create(ctx)` with Atlas `atlas migrate apply`
- [ ] Add `atlas migrate diff` step to CI for schema drift detection
- [ ] Document migration workflow in README

### P1 — Should Ship

#### 5. Platform Admin vs Tenant Admin Route Separation
- [ ] Add `/admin/plans` CRUD routes (create, update, deactivate plan) — platform admin only
- [ ] Add `/admin/tenants/{tenant_id}/subscription/override` — manual feature overrides
- [ ] Enforce platform admin role check via `shared-auth-client` claims
- [ ] Tenant routes (`/tenants/{tenant_id}/*`) remain scoped to tenant admin

#### 6. Subscription Status Validation Middleware
- [ ] Add middleware that rejects requests when subscription is EXPIRED or CANCELLED
- [ ] Return 402 Payment Required with upgrade prompt
- [ ] Exclude health/metric endpoints
- [ ] Configurable grace period (default: 7 days past expiry)

#### 7. Usage Tracking Skeleton ✅ DONE
- [x] `POST /api/v1/usage/report` — `UsageHandler.ReportUsage` (metric_type, service_name, value, period)
- [x] `GET /api/v1/usage` — `UsageHandler.GetUsageSummary` (aggregated by metric_type with from/to/service filters)
- [x] Ent schema `UsageEvent` added (`internal/ent/schema/usage_event.go`) — needs `go generate ./internal/ent/...`
- [x] Handler uses raw SQL on `usage_events` table (compiles before codegen; table created after migration)
- [ ] No overage calculation — ingestion only as designed

### P2 — Nice to Have

#### 8. OpenAPI Spec Generation ✅ DONE
- [x] Add swaggo annotations to all handlers ✅ DONE (May 2026)
- [x] Generate `openapi.yaml` via `swag init` ✅ DONE
- [x] Serve Swagger UI at `/docs/`

#### 9. Structured Error Responses
- [ ] Standardize error format: `{"error": "...", "code": "...", "details": {}}`
- [ ] Add error codes for common failures (TENANT_HAS_SUBSCRIPTION, PLAN_NOT_FOUND, etc.)

---

## Definition of Done

- [ ] All P0 items completed and deployed to staging
- [ ] Feature gate caching verified: < 10ms p95 on cached checks
- [ ] JWT claims enrichment verified end-to-end with auth-service
- [ ] `auth.tenant.created` consumer tested with NATS publish
- [ ] Atlas migration running in CI/CD pipeline
- [ ] No regressions: existing endpoints return same response shape
- [ ] `go build ./...` and `go test ./...` pass

---

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Atlas migration breaks existing data | High | Generate baseline from current schema; test on staging DB clone |
| NATS consumer misses events on restart | Medium | Use JetStream durable consumer with explicit ACK |
| Redis cache stale after subscription change | Medium | Invalidate on every mutation; short TTL (60s) |
| Auth-service JWT format mismatch | High | Define contract test; verify with `urban-loft` tenant |

---

## Dependencies

- `shared-events` library for NATS consumer (already used for publisher)
- `shared-auth-client` for platform admin role enforcement
- Auth-service must call `GET /tenants/{tenant_id}/subscription` during token issuance
- Atlas CLI binary in Docker image and CI runner
