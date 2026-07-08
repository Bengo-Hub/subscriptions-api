package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/bengobox/subscription-service/internal/app"
	_ "github.com/bengobox/subscription-service/internal/http/docs"
)

// @title Subscriptions API
// @version 1.0
// @description HTTP API for the Codevertex Subscriptions Service — manages subscription plans, features, usage, billing, and tenant entitlements.
// @host pricingapi.codevertexitsolutions.com
// @BasePath /api/v1
// @schemes https http
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description JWT Bearer token from SSO (e.g. "Bearer eyJ...")
// @securityDefinitions.apikey ApiKeyAuth
// @in header
// @name X-API-Key
// @description Service-to-service API key
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx)
	if err != nil {
		log.Fatalf("failed to initialise app: %v", err)
	}
	defer a.Close()

	if err := a.Run(ctx); err != nil {
		log.Fatalf("runtime error: %v", err)
	}
}

