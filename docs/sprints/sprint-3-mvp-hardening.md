# Sprint 3: MVP Hardening

**Sprint**: 3
**Deadline**: March 17, 2026
**Goal**: Production-ready feature gating, JWT claims enrichment, event-driven provisioning, Atlas migration transition

---

## Context

Sprints 1-2 delivered the database schema, seed data, CRUD endpoints, state machine, and outbox pattern. Sprint 3 hardens the service for the BengoBox MVP launch (March 17). The subscription-api is a critical-path dependency — every other service relies on it for Trinity Authorization (RBAC + Licensing + Resources).

---

## Deliverables

### P0 — Must Ship

#### 1. Redis Feature-Gate Caching
- [ ] Implement cache-aside pattern in `CheckFeature` handler
- [ ] Cache key: `subscription:feature:{tenant_id}:{feature_code}` with 60s TTL
- [ ] Cache full tenant entitlements on `GET /tenants/{tenant_id}/subscription`
- [ ] Invalidate cache on subscription mutations (create, change plan, cancel, renew)
- [ ] Add Redis health to `/readyz` endpoint (already wired, verify correctness)

#### 2. JWT Claims Enrichment — End-to-End Verification
- [ ] Verify `GET /tenants/{tenant_id}/subscription` response format matches auth-service expectations
- [ ] Ensure `features[]` and `limits{}` are populated correctly for all 6 plans
- [ ] Test with `urban-loft` tenant: confirm GROWTH features/limits in JWT
- [ ] Add integration test: create subscription → verify response → simulate auth-service call

#### 3. `auth.tenant.created` Event Consumer
- [ ] Implement NATS JetStream consumer for `auth.tenant.created`
- [ ] On event: auto-create Starter subscription with 14-day trial + delivery bundle
- [ ] Idempotency: skip if tenant already has a subscription
- [ ] Publish `subscription.created` outbox event after provisioning
- [ ] Wire consumer startup in `app.go` alongside outbox publisher
- [ ] Test with manual NATS publish simulating tenant creation

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

#### 7. Usage Tracking Skeleton
- [ ] Add `POST /tenants/{tenant_id}/usage/report` endpoint (accept metric_type + value)
- [ ] Store in `usage_tracking` table (already in ERD, not yet in Ent schema)
- [ ] Add Ent schema for `usage_tracking` and `usage_snapshots`
- [ ] No overage calculation yet — just ingestion

### P2 — Nice to Have

#### 8. OpenAPI Spec Generation
- [ ] Add swaggo annotations to all handlers (partially done)
- [ ] Generate `openapi.yaml` via `swag init`
- [ ] Serve Swagger UI at `/docs/`

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
