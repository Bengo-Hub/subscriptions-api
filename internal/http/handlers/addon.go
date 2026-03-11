package handlers

import (
	"github.com/bengobox/subscription-service/internal/ent"
	"go.uber.org/zap"
)

type AddonHandler struct {
	log    *zap.Logger
	client *ent.Client
}

func NewAddonHandler(log *zap.Logger, client *ent.Client) *AddonHandler {
	return &AddonHandler{
		log:    log.Named("addon.handler"),
		client: client,
	}
}
// Placeholder for other methods
