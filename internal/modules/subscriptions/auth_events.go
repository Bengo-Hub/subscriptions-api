package subscriptions

import (
	"context"
	"encoding/json"
	"fmt"

	eventslib "github.com/Bengo-Hub/shared-events"
	"github.com/bengobox/subscription-service/internal/ent/emaillicense"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"
)

// authUserCreatedPayload is the subset of auth.user.created's fields this
// consumer needs — matches the real event shape (see
// notifications-api/internal/modules/identity/events.go's AuthUserCreatedEvent).
type authUserCreatedPayload struct {
	UserID   string `json:"user_id"`
	TenantID string `json:"tenant_id"`
	Email    string `json:"email"`
}

// SubscribeAuthEvents wires the auth.user.created consumer. Per plan Part 3E's
// bounded-context design, auto-provisioning a mailbox on hire belongs here
// (where the licensing business logic already lives), not in
// email-provisioner's dumb NATS-to-Stalwart bridge — this only ever touches
// EmailLicense rows and emits the existing email.license.assigned event;
// email-provisioner's already-deployed consumer does the actual Stalwart call.
func (s *Service) SubscribeAuthEvents(nc *nats.Conn) error {
	_, err := eventslib.QueueSubscribe(s.log, nc, "auth.user.created", "subscriptions-auth-user-created", s.handleAuthUserCreated)
	if err != nil {
		return fmt.Errorf("subscribe auth.user.created: %w", err)
	}
	return nil
}

func (s *Service) handleAuthUserCreated(msg *nats.Msg) {
	ctx := context.Background()

	ev, err := eventslib.FromJSON(msg.Data)
	if err != nil {
		s.log.Error("decode auth.user.created event failed", zap.Error(err))
		return
	}

	var p authUserCreatedPayload
	if b, err := json.Marshal(ev.Payload); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	if p.TenantID == "" || p.Email == "" {
		return
	}
	tenantID, err := uuid.Parse(p.TenantID)
	if err != nil {
		s.log.Warn("auth.user.created: invalid tenant_id, skipping", zap.String("tenant_id", p.TenantID))
		return
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		s.log.Error("auth.user.created: begin tx failed", zap.Error(err))
		return
	}

	tenantSub, err := tx.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		// The overwhelming majority of tenants have no email-hosting
		// subscription at all yet — this is the expected, common case,
		// not a failure worth logging above debug.
		_ = tx.Rollback()
		return
	}

	// Assign the first AVAILABLE license, if any — a tenant that hasn't
	// purchased/has exhausted its email seats is equally common; nothing to
	// do, not an error.
	lic, err := tx.EmailLicense.Query().
		Where(emaillicense.TenantSubscriptionIDEQ(tenantSub.ID), emaillicense.StatusEQ("AVAILABLE")).
		First(ctx)
	if err != nil {
		_ = tx.Rollback()
		return
	}

	userID, parseErr := uuid.Parse(p.UserID)
	update := tx.EmailLicense.UpdateOneID(lic.ID).
		SetAssignedToEmail(p.Email).
		SetStatus("ASSIGNED")
	if parseErr == nil {
		update = update.SetAssignedToUserID(userID)
	}
	updated, err := update.Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		s.log.Error("auth.user.created: assign email license failed", zap.Error(err))
		return
	}

	s.writeOutboxEvent(ctx, tx, tenantID, "email", lic.ID, "license.assigned", map[string]any{
		"license_id":        lic.ID.String(),
		"assigned_to_email": p.Email,
		"domain":            "codevertexafrica.com",
		"storage_quota_gb":  updated.StorageQuotaGB,
	})

	if err := tx.Commit(); err != nil {
		s.log.Error("auth.user.created: commit email license assignment failed", zap.Error(err))
		return
	}
	s.log.Info("auto-assigned email license on user creation",
		zap.String("tenant_id", p.TenantID), zap.String("email", p.Email))
}
