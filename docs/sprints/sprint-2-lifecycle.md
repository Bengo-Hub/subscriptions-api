# Sprint 2: Subscription Lifecycle & Events

**Sprint**: 2
**Dates**: February 14, 2026
**Goal**: State machine, event publishing, outbox pattern

---

## Completed

### State Machine
Subscription states: `trialing` → `active` → `past_due` → `cancelled` / `expired`

Valid transitions:
- `trialing` → `active` (on payment)
- `trialing` → `cancelled` (user cancels trial)
- `trialing` → `expired` (trial period ends)
- `active` → `past_due` (payment failed)
- `active` → `cancelled` (user cancels)
- `past_due` → `active` (payment retried)
- `past_due` → `cancelled` (max retries)

### Transactional Outbox Pattern
- Domain writes + outbox event in single transaction
- Outbox publisher polls for PENDING events
- Publishes to NATS `subscriptions.events` subject
- Marks as PUBLISHED on success, retries on failure

### Events Published
- `subscription.created` — New subscription (trial or paid)
- `subscription.activated` — Payment confirmed, subscription active
- `subscription.cancelled` — User or system cancellation
- `subscription.expired` — Trial or subscription period ended
- `subscription.upgraded` — Bundle tier upgrade
- `subscription.downgraded` — Bundle tier downgrade

### Build Status
- `go build ./...` — 0 errors
