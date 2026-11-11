package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Credit bundles carry VAT only when the platform itself charges it (owner, 2026-11-11): its
// treasury tax profile has VAT active (VAT-registered with auto-charge on) and eTIMS activated.
// treasury-api owns that decision (GET /api/v1/s2s/{platform}/tax-profile); this only reads it,
// cached for a few minutes. Any failure means no VAT: a bundle is never over-charged because
// treasury was unreachable.

const platformVATCacheFor = 10 * time.Minute

type platformVATCache struct {
	mu      sync.Mutex
	charges bool
	at      time.Time
}

var platformVAT platformVATCache

// platformChargesVAT decodes a treasury tax-profile body.
func platformChargesVAT(body []byte) bool {
	var p struct {
		VatActive      bool `json:"vat_active"`
		EtimsActivated bool `json:"etims_activated"`
	}
	if json.Unmarshal(body, &p) != nil {
		return false
	}
	return p.VatActive && p.EtimsActivated
}

// PlatformVATRate is the VAT percentage to add on top of a credit bundle: the platform VAT rate
// when the platform charges VAT, otherwise 0.
func (s *InvoiceService) PlatformVATRate(ctx context.Context) float64 {
	if s == nil || s.treasury == nil || s.platformTenantID == "" || s.vatRate <= 0 {
		return 0
	}
	platformVAT.mu.Lock()
	if time.Since(platformVAT.at) < platformVATCacheFor {
		charges := platformVAT.charges
		platformVAT.mu.Unlock()
		if charges {
			return s.vatRate
		}
		return 0
	}
	platformVAT.mu.Unlock()

	charges := false
	resp, err := s.treasury.Get(ctx, fmt.Sprintf("/api/v1/s2s/%s/tax-profile", s.platformTenantID), s.headers())
	if err == nil && resp != nil && resp.StatusCode == 200 {
		charges = platformChargesVAT(resp.Body)
	}
	platformVAT.mu.Lock()
	platformVAT.charges, platformVAT.at = charges, time.Now()
	platformVAT.mu.Unlock()
	if charges {
		return s.vatRate
	}
	return 0
}
