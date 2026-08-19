package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/tenantsubscription"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/modules/subscriptions"
)

// WebhookHandler handles inbound webhooks from internal services (e.g. Treasury).
type WebhookHandler struct {
	log            *zap.Logger
	orm            *ent.Client
	svc            *subscriptions.Service
	apiKey         string
	featureHandler *FeatureHandler
}

// NewWebhookHandler creates a new WebhookHandler.
func NewWebhookHandler(log *zap.Logger, orm *ent.Client, svc *subscriptions.Service, apiKey string, featureHandler *FeatureHandler) *WebhookHandler {
	return &WebhookHandler{
		log:            log.Named("webhook.handler"),
		orm:            orm,
		svc:            svc,
		apiKey:         apiKey,
		featureHandler: featureHandler,
	}
}

// HandleTreasuryPayment godoc
// @Summary Treasury payment webhook (S2S)
// @Description Receives payment completion/failure events from Treasury to activate or suspend subscriptions.
// @Tags Webhooks
// @Accept json
// @Produce json
// @Security ApiKeyAuth
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /webhooks/treasury/payment-status [post]
func (h *WebhookHandler) HandleTreasuryPayment(w http.ResponseWriter, r *http.Request) {
	// Validate S2S API key
	if h.apiKey != "" && r.Header.Get("X-API-Key") != h.apiKey {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var body struct {
		PaymentIntentID string  `json:"payment_intent_id"`
		Status          string  `json:"status"` // "completed" or "failed"
		TenantID        string  `json:"tenant_id"`
		PlanCode        string  `json:"plan_code"`
		ReferenceType   string  `json:"reference_type"`
		Amount          float64 `json:"amount"`
		Currency        string  `json:"currency"`
		// Card authorization data — populated on charge.success when Paystack returns a reusable card.
		PaystackAuthCode string `json:"paystack_auth_code"`
		CardLast4        string `json:"card_last4"`
		CardType         string `json:"card_type"`
		CardExpMonth     string `json:"card_exp_month"`
		CardExpYear      string `json:"card_exp_year"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if body.TenantID == "" || body.Status == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant_id and status are required"})
		return
	}

	tenantID, err := uuid.Parse(body.TenantID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tenant_id"})
		return
	}

	ctx := r.Context()

	switch body.Status {
	case "completed":
		// For card_setup intents: store authorization_code for auto-renewal, no subscription state change.
		if body.ReferenceType == "card_setup" {
			if body.PaystackAuthCode != "" {
				if err := h.saveCardAuthorization(ctx, tenantID, body.PaystackAuthCode, body.CardType, body.CardLast4, body.CardExpMonth, body.CardExpYear); err != nil {
					h.log.Error("failed to save card authorization", zap.String("tenant_id", body.TenantID), zap.Error(err))
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save card authorization"})
					return
				}
				h.log.Info("card authorization saved for auto-renewal", zap.String("tenant_id", body.TenantID), zap.String("last4", body.CardLast4))
			}
			break
		}
		// email_license_purchase: a discrete, one-off paid seat purchase —
		// fulfill (provision the AVAILABLE EmailLicense rows) rather than
		// falling through to activateSubscription, which would misinterpret
		// this as a plan renewal.
		if body.ReferenceType == "email_license_purchase" {
			if err := h.fulfillEmailLicensePurchase(ctx, tenantID, body.PaymentIntentID); err != nil {
				h.log.Error("failed to fulfill email license purchase via webhook", zap.String("tenant_id", body.TenantID), zap.Error(err))
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fulfill email license purchase"})
				return
			}
			break
		}
		// For subscription / renewal intents: activate/extend subscription period.
		if err := h.activateSubscription(ctx, tenantID, body.PlanCode); err != nil {
			h.log.Error("failed to activate subscription via webhook", zap.String("tenant_id", body.TenantID), zap.Error(err))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to activate subscription"})
			return
		}
		// If a card authorization arrived with the renewal, persist it.
		if body.PaystackAuthCode != "" {
			_ = h.saveCardAuthorization(ctx, tenantID, body.PaystackAuthCode, body.CardType, body.CardLast4, body.CardExpMonth, body.CardExpYear)
		}
		if h.featureHandler != nil {
			h.featureHandler.InvalidateCache(ctx, tenantID)
		}
		h.log.Info("subscription activated via treasury webhook", zap.String("tenant_id", body.TenantID), zap.String("plan_code", body.PlanCode))

	case "failed":
		if body.ReferenceType == "card_setup" {
			h.log.Warn("card setup failed", zap.String("tenant_id", body.TenantID))
			break
		}
		if err := h.markPaymentRequired(ctx, tenantID); err != nil {
			h.log.Error("failed to mark payment_required via webhook", zap.String("tenant_id", body.TenantID), zap.Error(err))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update subscription"})
			return
		}
		if h.featureHandler != nil {
			h.featureHandler.InvalidateCache(ctx, tenantID)
		}
		h.log.Info("subscription marked payment_required via treasury webhook", zap.String("tenant_id", body.TenantID))

	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown status: " + body.Status})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "processed"})
}

// saveCardAuthorization stores a Paystack authorization_code in subscription metadata for auto-renewal.
func (h *WebhookHandler) saveCardAuthorization(ctx context.Context, tenantID uuid.UUID, authCode, cardType, last4, expMonth, expYear string) error {
	sub, err := h.orm.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		return err
	}

	meta := make(map[string]any)
	for k, v := range sub.Metadata {
		meta[k] = v
	}
	newMethod := map[string]any{
		"type":        "card",
		"brand":       cardType,
		"last4":       last4,
		"expiryMonth": expMonth,
		"expiryYear":  expYear,
	}
	meta["paystack_auth_code"] = authCode
	meta["payment_method"] = newMethod
	// Maintain payment_methods array; append if not already present (deduplicate by last4).
	meta["payment_methods"] = appendPaymentMethod(meta["payment_methods"], newMethod, last4)

	_, err = h.orm.TenantSubscription.UpdateOneID(sub.ID).
		SetMetadata(meta).
		Save(ctx)
	return err
}

// fulfillEmailLicensePurchase reads back the confirmed intent's own metadata
// (plan_code/quantity) and provisions the AVAILABLE EmailLicense rows. See
// Service.GetEmailLicensePurchaseIntentMetadata's own comment for why the
// intent, not this webhook body, is the source of truth for those fields.
func (h *WebhookHandler) fulfillEmailLicensePurchase(ctx context.Context, tenantID uuid.UUID, paymentIntentIDStr string) error {
	intentID, err := uuid.Parse(paymentIntentIDStr)
	if err != nil {
		return err
	}
	planCode, quantity, err := h.svc.GetEmailLicensePurchaseIntentMetadata(ctx, tenantID, intentID)
	if err != nil {
		return err
	}
	_, err = h.svc.FulfillEmailLicensePurchase(ctx, tenantID, planCode, quantity)
	return err
}

func (h *WebhookHandler) activateSubscription(ctx context.Context, tenantID uuid.UUID, planCode string) error {
	now := time.Now().UTC()

	tx, err := h.orm.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	sub, err := tx.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		return err
	}

	// Respect the tenant's already-chosen billing cycle (MONTHLY/SEMI_ANNUAL/ANNUAL) —
	// a hardcoded +1 month here desyncs current_period_end from the months actually
	// invoiced by InvoiceService.buildLines for longer cycles.
	periodEnd := subscriptions.AddBillingCycle(now, string(sub.BillingCycle))

	update := tx.TenantSubscription.UpdateOneID(sub.ID).
		SetStatus(tenantsubscription.StatusACTIVE).
		SetCurrentPeriodStart(now).
		SetCurrentPeriodEnd(periodEnd)

	sub, err = update.Save(ctx)
	if err != nil {
		return err
	}

	h.svc.WriteOutboxEventPublic(ctx, tx, tenantID, "subscription", sub.ID, "activated", map[string]any{
		"tenant_id": tenantID.String(),
		"plan_code": planCode,
		"status":    "ACTIVE",
		"notification": map[string]any{
			"target": "tenant_admin",
		},
	})

	// Publish tenant_activated for CRM sync — MarketFlow upserts tenant admin as Contact
	h.svc.WriteOutboxEventPublic(ctx, tx, tenantID, "subscription", sub.ID, "tenant_activated", map[string]any{
		"tenant_id":  tenantID.String(),
		"plan_code":  planCode,
	})

	return tx.Commit()
}

func (h *WebhookHandler) markPaymentRequired(ctx context.Context, tenantID uuid.UUID) error {
	tx, err := h.orm.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	sub, err := tx.TenantSubscription.Query().
		Where(tenantsubscription.TenantIDEQ(tenantID)).
		Only(ctx)
	if err != nil {
		return err
	}

	// SUSPENDED status represents payment_required (no PAYMENT_REQUIRED status in schema)
	sub, err = tx.TenantSubscription.UpdateOneID(sub.ID).
		SetStatus(tenantsubscription.StatusSUSPENDED).
		Save(ctx)
	if err != nil {
		return err
	}

	h.svc.WriteOutboxEventPublic(ctx, tx, tenantID, "subscription", sub.ID, "payment_required", map[string]any{
		"tenant_id": tenantID.String(),
		"status":    "SUSPENDED",
		"notification": map[string]any{
			"target": "tenant_admin",
		},
	})

	return tx.Commit()
}
