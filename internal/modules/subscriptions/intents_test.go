package subscriptions

import (
	"errors"
	"strings"
	"testing"

	serviceclient "github.com/Bengo-Hub/shared-service-client"
	"github.com/google/uuid"
)

// A refused intent must say why. Treasury's 403 carries no Go error, which used to print
// "payment initiation failed: %!w(<nil>)".
func TestTreasuryFailureSaysWhy(t *testing.T) {
	refused := &serviceclient.Response{StatusCode: 403,
		Body: []byte(`{"error":"platform billing payments are created by the platform, not by tenant users"}`)}
	got := TreasuryFailure("payment initiation failed", refused, nil).Error()
	if strings.Contains(got, "%!") || !strings.Contains(got, "403") || !strings.Contains(got, "created by the platform") {
		t.Fatalf("unexpected message %q", got)
	}
	if got := TreasuryFailure("x", &serviceclient.Response{StatusCode: 502, Body: []byte("<html>")}, nil).Error(); got != "x: payment service answered 502" {
		t.Fatalf("unexpected message %q", got)
	}
	if got := TreasuryFailure("x", nil, errors.New("dial tcp: refused")).Error(); got != "x: dial tcp: refused" {
		t.Fatalf("unexpected message %q", got)
	}
}

// Platform purchases go through Treasury's S2S route, and every one-off purchase gets its own
// reference so Treasury never hands back an earlier purchase's intent.
func TestPurchaseIntentsUseS2SAndUniqueReferences(t *testing.T) {
	tenant := uuid.MustParse("5bce71cd-a29f-484f-adc7-3566aed6d14f")
	if got := TreasuryIntentsPath(tenant); got != "/api/v1/s2s/5bce71cd-a29f-484f-adc7-3566aed6d14f/payments/intents" {
		t.Fatalf("intents path %q", got)
	}
	a, b := NewPurchaseReference("TOKTOP", tenant, "ai"), NewPurchaseReference("TOKTOP", tenant, "ai")
	if a == b || !strings.HasPrefix(a, "TOKTOP-5bce71cd-ai-") {
		t.Fatalf("references %q and %q", a, b)
	}
}
