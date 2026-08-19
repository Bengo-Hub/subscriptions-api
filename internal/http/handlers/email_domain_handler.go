package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/subscription-service/internal/ent"
	"github.com/bengobox/subscription-service/internal/ent/emaillicense"
	"github.com/bengobox/subscription-service/internal/ent/tenantemaildomain"
)

// Self-service custom-domain onboarding (plan Part 1.3). Split from
// email_handler.go (already over this codebase's ~400-line file-length
// convention) — same EmailLicenseHandler type, methods split across files
// per this package's own established pattern.

type createDomainInput struct {
	Domain string `json:"domain"`
}

// emailProvisionerCreateDomainResponse is what email-provisioner's
// POST /internal/domains returns after provisioning the domain in Stalwart.
type emailProvisionerCreateDomainResponse struct {
	StalwartDomainID string `json:"stalwart_domain_id"`
	DKIMSelector     string `json:"dkim_selector"`
	DNSZoneFile      string `json:"dns_zone_file"`
}

// emailProvisionerUsageSummary mirrors email-provisioner's DomainUsageSummary.
type emailProvisionerUsageSummary struct {
	MailboxCount int `json:"mailbox_count"`
	Mailboxes    []struct {
		Email         string `json:"email"`
		UsedDiskQuota int64  `json:"used_disk_quota"`
	} `json:"mailboxes"`
}

// MailboxUsage is one of the caller's own mailboxes' real usage — returned by
// GetEmailUsageSummary.
type MailboxUsage struct {
	Email          string `json:"email"`
	UsedBytes      int64  `json:"used_bytes"`
	AllocatedBytes int64  `json:"allocated_bytes"`
}

// GetEmailUsageSummary handles GET /api/v1/email/usage-summary — real
// per-mailbox storage usage for the tenant dashboard (plan Part 2b/5, T4).
// Derives the domain(s) to query from the tenant's OWN assigned licenses
// (never a caller-supplied domain param) and filters email-provisioner's
// response down to exactly those emails before returning — a shared domain
// (the platform default) must never leak another tenant's mailbox list,
// even if email-provisioner's own response were ever broader than expected.
func (h *EmailLicenseHandler) GetEmailUsageSummary(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}
	ctx := r.Context()

	tenantSubID, err := h.resolveTenantSubscriptionID(ctx, h.client, tenantID)
	if err != nil {
		writeJSON(w, http.StatusOK, []MailboxUsage{})
		return
	}

	licenses, err := h.client.EmailLicense.Query().
		Where(emaillicense.TenantSubscriptionIDEQ(tenantSubID), emaillicense.StatusEQ("ASSIGNED")).
		All(ctx)
	if err != nil {
		h.log.Error("list assigned licenses for usage summary failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	// allocatedByEmail + domains this tenant actually owns mailboxes on.
	allocatedByEmail := map[string]int64{}
	domains := map[string]bool{}
	for _, lic := range licenses {
		if lic.AssignedToEmail == nil {
			continue
		}
		email := strings.ToLower(*lic.AssignedToEmail)
		allocatedByEmail[email] = int64(lic.StorageQuotaGB) * 1024 * 1024 * 1024
		if parts := strings.SplitN(email, "@", 2); len(parts) == 2 {
			domains[parts[1]] = true
		}
	}

	usedByEmail := map[string]int64{}
	if h.emailProvisionerClient != nil {
		for domain := range domains {
			resp, err := h.emailProvisionerClient.Get(ctx, fmt.Sprintf("/internal/domains/%s/usage-summary", domain), nil)
			if err != nil || !resp.IsSuccess() {
				h.log.Warn("fetch domain usage summary failed", zap.String("domain", domain), zap.Error(err))
				continue
			}
			var summary emailProvisionerUsageSummary
			if err := resp.DecodeJSON(&summary); err != nil {
				continue
			}
			for _, m := range summary.Mailboxes {
				usedByEmail[strings.ToLower(m.Email)] = m.UsedDiskQuota
			}
		}
	}

	out := make([]MailboxUsage, 0, len(allocatedByEmail))
	for email, allocated := range allocatedByEmail {
		// Only ever report an email this tenant's own licenses actually assigned —
		// usedByEmail is a lookup table, never the source of which rows to emit.
		out = append(out, MailboxUsage{Email: email, UsedBytes: usedByEmail[email], AllocatedBytes: allocated})
	}
	writeJSON(w, http.StatusOK, out)
}

// ListEmailDomains handles GET /api/v1/email/domains — the caller's own tenant only.
func (h *EmailLicenseHandler) ListEmailDomains(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}
	ctx := r.Context()

	tenantSubID, err := h.resolveTenantSubscriptionID(ctx, h.client, tenantID)
	if err != nil {
		writeJSON(w, http.StatusOK, []any{}) // no subscription yet = no domains, not an error
		return
	}

	domains, err := h.client.TenantEmailDomain.Query().
		Where(tenantemaildomain.TenantSubscriptionIDEQ(tenantSubID)).
		All(ctx)
	if err != nil {
		h.log.Error("list email domains failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, domains)
}

// CreateEmailDomain handles POST /api/v1/email/domains. Calls email-provisioner
// (the sole JMAP speaker — subscriptions-api never talks to Stalwart directly)
// to provision the domain in Stalwart, then persists the returned DNS records
// as a PENDING_DNS row for the tenant to publish and verify.
func (h *EmailLicenseHandler) CreateEmailDomain(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}

	var in createDomainInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Domain == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "domain is required"})
		return
	}
	in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))

	ctx := r.Context()
	tenantSubID, err := h.resolveTenantSubscriptionID(ctx, h.client, tenantID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tenant has no active subscription"})
		return
	}

	if h.emailProvisionerClient == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "domain provisioning service unavailable"})
		return
	}

	resp, err := h.emailProvisionerClient.Post(ctx, "/internal/domains", map[string]any{"domain": in.Domain}, nil)
	if err != nil || !resp.IsSuccess() {
		h.log.Error("provision domain via email-provisioner failed", zap.String("domain", in.Domain), zap.Error(err))
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to provision domain"})
		return
	}
	var provResp emailProvisionerCreateDomainResponse
	if err := resp.DecodeJSON(&provResp); err != nil {
		h.log.Error("decode email-provisioner create-domain response failed", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	created, err := h.client.TenantEmailDomain.Create().
		SetTenantSubscriptionID(tenantSubID).
		SetDomain(in.Domain).
		SetStatus("PENDING_DNS").
		SetDkimSelector(provResp.DKIMSelector).
		SetStalwartDomainID(provResp.StalwartDomainID).
		SetMetadata(map[string]any{"dns_zone_file": provResp.DNSZoneFile}).
		Save(ctx)
	if err != nil {
		h.log.Error("create tenant email domain failed", zap.String("domain", in.Domain), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

// VerifyEmailDomain handles POST /api/v1/email/domains/{id}/verify. Runs live
// public DNS lookups (MX/SPF/DKIM/DMARC) — this platform doesn't control the
// tenant's own DNS, so "verification" means confirming what they've actually
// published, not anything Stalwart-side.
func (h *EmailLicenseHandler) VerifyEmailDomain(w http.ResponseWriter, r *http.Request) {
	domainRowID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid domain id"})
		return
	}
	tenantID, ok := h.tenantID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "tenant context required"})
		return
	}

	ctx := r.Context()
	tenantSubID, err := h.resolveTenantSubscriptionID(ctx, h.client, tenantID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "domain not found"})
		return
	}

	dom, err := h.client.TenantEmailDomain.Query().
		Where(tenantemaildomain.ID(domainRowID), tenantemaildomain.TenantSubscriptionIDEQ(tenantSubID)).
		Only(ctx)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "domain not found"})
		return
	}

	selector := ""
	if dom.DkimSelector != nil {
		selector = *dom.DkimSelector
	}
	ok2, reason := h.checkDomainDNS(dom.Domain, selector)

	now := time.Now().UTC()
	update := h.client.TenantEmailDomain.UpdateOneID(domainRowID).SetLastCheckedAt(now)
	if ok2 {
		update = update.SetStatus("VERIFIED").SetVerifiedAt(now).ClearFailureReason()
	} else {
		update = update.SetStatus("FAILED").SetFailureReason(reason)
	}
	updated, err := update.Save(ctx)
	if err != nil {
		h.log.Error("verify email domain failed", zap.String("domain", dom.Domain), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// platformSharedDomain is the shared Codevertex-hosted domain every tenant
// may assign licenses on regardless of custom-domain onboarding status.
const platformSharedDomain = "codevertexafrica.com"

// emailDomainAllowed enforces the domain gate for AssignEmailLicense: the
// target address's domain must be the shared platform domain, or one of
// this tenant's own TenantEmailDomain rows with status=VERIFIED.
func (h *EmailLicenseHandler) emailDomainAllowed(ctx context.Context, tenantID uuid.UUID, email string) (bool, string) {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[1] == "" {
		return false, "invalid email address"
	}
	domain := strings.ToLower(parts[1])
	if domain == platformSharedDomain {
		return true, ""
	}

	tenantSubID, err := h.resolveTenantSubscriptionID(ctx, h.client, tenantID)
	if err != nil {
		return false, fmt.Sprintf("domain %q is not the shared platform domain and tenant has no onboarded domains", domain)
	}

	dom, err := h.client.TenantEmailDomain.Query().
		Where(tenantemaildomain.DomainEQ(domain), tenantemaildomain.TenantSubscriptionIDEQ(tenantSubID)).
		Only(ctx)
	if err != nil {
		return false, fmt.Sprintf("domain %q has not been onboarded by this tenant", domain)
	}
	if dom.Status != "VERIFIED" {
		return false, fmt.Sprintf("domain %q is onboarded but not yet verified (status=%s)", domain, dom.Status)
	}
	return true, ""
}

// MarkEmailLicenseDeleted handles POST /api/v1/admin/email/licenses/{id}/mark-deleted.
// Platform-admin-only, called by mail-ui's admin console immediately after a
// successful synchronous Stalwart destroy (DELETE /internal/mailboxes/{email}
// on email-provisioner) — keeps the EmailLicense row from silently
// disagreeing with reality once the underlying mailbox is permanently gone.
// This is the only place a license is allowed to move to DELETED — a
// terminal state, unlike SUSPENDED/EXPIRED which can still transition
// elsewhere. Platform-ops bypasses tenant scoping entirely here, matching
// the access model's "unscoped" platform-admin capability.
func (h *EmailLicenseHandler) MarkEmailLicenseDeleted(w http.ResponseWriter, r *http.Request) {
	if !h.isTrustedAdminCaller(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "platform owner access required"})
		return
	}
	licenseID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid license id"})
		return
	}

	ctx := r.Context()
	updated, err := h.client.EmailLicense.UpdateOneID(licenseID).
		SetStatus("DELETED").
		Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "license not found"})
			return
		}
		h.log.Error("mark email license deleted failed", zap.String("license_id", licenseID.String()), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// MarkEmailLicenseDeletedByEmail handles POST /api/v1/admin/email/licenses/mark-deleted-by-email.
// mail-ui's admin console only knows the mailbox's email address, not any
// subscriptions-api license id — this closes that gap (plan Part 5, T1) so
// a platform-admin hard-delete can keep the license row in sync without a
// separate lookup round-trip. Best-effort by design: most hard-deletes won't
// have a matching license row at all yet (e.g. the platform's own mailboxes,
// or a mailbox created before licensing existed) — "not found" is a normal,
// silent no-op here, not an error, since the caller's own destroy already
// succeeded and shouldn't be reported as failed over this side-effect.
func (h *EmailLicenseHandler) MarkEmailLicenseDeletedByEmail(w http.ResponseWriter, r *http.Request) {
	if !h.isTrustedAdminCaller(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "platform owner access required"})
		return
	}
	var in struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
		return
	}

	ctx := r.Context()
	ids, err := h.client.EmailLicense.Query().
		Where(emaillicense.AssignedToEmailEQ(in.Email)).
		IDs(ctx)
	if err != nil {
		h.log.Error("lookup email license by email failed", zap.String("email", in.Email), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"updated": 0})
		return
	}

	n, err := h.client.EmailLicense.Update().
		Where(emaillicense.IDIn(ids...)).
		SetStatus("DELETED").
		Save(ctx)
	if err != nil {
		h.log.Error("mark email license deleted by email failed", zap.String("email", in.Email), zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": n})
}

// checkDomainDNS is a pure-ish function (only I/O is the DNS lookups
// themselves) so the pass/fail logic — which records are required and how
// they're matched — is easy to reason about and could be unit-tested against
// a fake resolver later. Returns (verified, failureReason).
func (h *EmailLicenseHandler) checkDomainDNS(domain, dkimSelector string) (bool, string) {
	var missing []string

	mxRecords, err := net.LookupMX(domain)
	if err != nil || !mxHostMatches(mxRecords, h.platformMailMXHost) {
		missing = append(missing, fmt.Sprintf("MX (expected %s)", h.platformMailMXHost))
	}

	// Stalwart's own auto-generated SPF for a newly onboarded domain is
	// "v=spf1 mx -all" (confirmed live via a disposable domain probe,
	// 2026-08-19) — an `mx` mechanism, not `ip4:<platform-ip>`. Since the
	// domain's own MX already points at the platform (checked above), `mx`
	// correctly authorizes it; accept either mechanism rather than requiring
	// the literal platform IP, which would reject Stalwart's own default.
	spfTXT, _ := net.LookupTXT(domain)
	spfHasMechanism := anyContains(spfTXT, " mx") || anyContains(spfTXT, "ip4:"+h.platformMailIP)
	if !anyContains(spfTXT, "v=spf1") || !spfHasMechanism {
		missing = append(missing, "SPF (expected v=spf1 with an mx or ip4 mechanism authorizing this platform)")
	}

	if dkimSelector != "" {
		dkimTXT, _ := net.LookupTXT(fmt.Sprintf("%s._domainkey.%s", dkimSelector, domain))
		if !anyContains(dkimTXT, "v=DKIM1") {
			missing = append(missing, fmt.Sprintf("DKIM at selector %s", dkimSelector))
		}
	}

	dmarcTXT, _ := net.LookupTXT("_dmarc." + domain)
	if !anyContains(dmarcTXT, "v=DMARC1") {
		missing = append(missing, "DMARC")
	}

	if len(missing) > 0 {
		return false, "missing or incorrect records: " + strings.Join(missing, ", ")
	}
	return true, ""
}

func mxHostMatches(records []*net.MX, expectedHost string) bool {
	expected := strings.TrimSuffix(strings.ToLower(expectedHost), ".")
	for _, r := range records {
		if strings.TrimSuffix(strings.ToLower(r.Host), ".") == expected {
			return true
		}
	}
	return false
}

func anyContains(records []string, substr string) bool {
	for _, r := range records {
		if strings.Contains(r, substr) {
			return true
		}
	}
	return false
}
