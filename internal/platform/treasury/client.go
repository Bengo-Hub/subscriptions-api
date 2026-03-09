package treasury

import (
	"context"
	"fmt"

	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Client provides methods to interact with the treasury service for intent creation.
type Client struct {
	serviceClient *serviceclient.Client
	logger        *zap.Logger
}

// Config holds configuration for the Treasury API client.
type Config struct {
	ServiceURL string
}

// NewClient creates a new treasury service client.
func NewClient(cfg Config, logger *zap.Logger) *Client {
	scCfg := serviceclient.DefaultConfig(
		cfg.ServiceURL,
		"subscriptions-service",
		logger.Named("treasury.client"),
	)

	return &Client{
		serviceClient: serviceclient.New(scCfg),
		logger:        logger.Named("treasury.client"),
	}
}

// CreateIntentRequest represents a request to create a payment intent in Treasury API.
type CreateIntentRequest struct {
	ReferenceID    string                 `json:"reference_id"`
	ReferenceType  string                 `json:"reference_type"` // Always "subscription"
	PaymentMethod  string                 `json:"payment_method"`
	Currency       string                 `json:"currency"`
	Amount         float64                `json:"amount"`
	CustomerID     *uuid.UUID             `json:"customer_id,omitempty"`
	CustomerEmail  *string                `json:"customer_email,omitempty"`
	Description    *string                `json:"description,omitempty"`
	PhoneNumber    *string                `json:"phone_number,omitempty"`
	IdempotencyKey string                 `json:"idempotency_key,omitempty"`
	SourceService  string                 `json:"source_service,omitempty"` // Explicitly tracking income origin
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

// CreateIntentResponse represents the response after creating a payment intent.
type CreateIntentResponse struct {
	IntentID          uuid.UUID `json:"intent_id"`
	Status            string    `json:"status"`
	PaymentMethod     string    `json:"payment_method"`
	CheckoutRequestID *string   `json:"checkout_request_id,omitempty"` // M-Pesa
	AuthorizationURL  *string   `json:"authorization_url,omitempty"`   // Paystack
	AccessCode        *string   `json:"access_code,omitempty"`         // Paystack
}

// CreatePaymentIntent calls the Treasury API to orchestrate a central payment intent.
func (c *Client) CreatePaymentIntent(ctx context.Context, tenantID uuid.UUID, req CreateIntentRequest) (*CreateIntentResponse, error) {
	// Inject required Subscription properties so treasury can attribute income by source
	req.ReferenceType = "subscription"
	req.SourceService = "subscription"
	// Optionally set req.Metadata["product"] and req.Metadata["tier"] for product/tier attribution

	// Fallback to ensuring currency is KES if omitted
	if req.Currency == "" {
		req.Currency = "KES"
	}

	path := fmt.Sprintf("/%s/payments/intents", tenantID.String())

	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}

	if req.IdempotencyKey != "" {
		headers["Idempotency-Key"] = req.IdempotencyKey
	}

	resp, err := c.serviceClient.Post(ctx, path, req, headers)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("treasury api error, status %d: %s", resp.StatusCode, string(resp.Body))
	}

	var result CreateIntentResponse
	if err := resp.DecodeJSON(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &result, nil
}
