package subscriptions

import "errors"

// CurrentTermsVersion is the active subscription Terms & Conditions version a tenant must
// accept when subscribing to a plan. It is date-stamped; bump it whenever the terms change.
// Existing subscriptions retain the version they originally accepted (stored on the row).
const CurrentTermsVersion = "2026-06-20"

// ErrTermsNotAccepted is returned when a subscribe request does not carry a valid T&C
// acceptance (accepted flag + matching version). Surfaced as HTTP 400 by the handler.
var ErrTermsNotAccepted = errors.New("subscription terms and conditions must be accepted to subscribe")

// SubscriptionTerms is the human-readable Terms & Conditions shown in the subscribe flow and
// served by GET /api/v1/terms. Kept in source (versioned with the app) so the exact text a
// tenant accepted is auditable against the stored terms_version.
const SubscriptionTerms = `# Codevertex Subscription Terms & Conditions

Version 2026-06-20

By subscribing to a Codevertex plan you ("the Customer") agree to the following terms with
Codevertex Africa Limited ("Codevertex").

## 1. Service and fees
1.1 Codevertex provides point-of-sale and related business software on a subscription, one-time
licence, or pay-as-you-grow (Flex) basis as set out in your selected plan.
1.2 Subscription fees are billed in advance for the chosen billing cycle (monthly or annual) in
Kenya Shillings. One-time setup/implementation fees, where applicable, are billed once.
1.3 Flex (pay-as-you-grow) charges a service fee on each successful transaction, as published for
your plan, collected at the point of payment.

## 2. Billing, renewal and non-payment
2.1 Subscriptions renew automatically at the end of each billing cycle unless cancelled.
2.2 If payment fails, a grace period applies after which access may be suspended until payment is
made.

## 3. Account dormancy and data retention
3.1 An account is considered dormant if it records no billable activity for more than 60
consecutive days and no minimum applicable fee is paid during that period.
3.2 Codevertex will notify the Customer when an account becomes dormant. The Customer then has a
grace period of 7 days to reactivate the account (by recording activity or paying the applicable
fee).
3.3 If the account is not reactivated within the grace period, it is suspended and queued for
removal. The Customer's data may then be permanently deleted. The Customer is responsible for
exporting any data they wish to keep before the grace period ends.

## 4. Customer obligations
4.1 The Customer is responsible for the accuracy of data entered, for meeting their own tax
obligations (including KRA eTIMS, ToT and VAT where applicable), and for keeping login
credentials secure.
4.2 The Customer shall not use the service for unlawful purposes.

## 5. Data protection
5.1 Codevertex processes Customer data to provide the service, in line with the Kenya Data
Protection Act, 2019. The Customer retains ownership of their business data.

## 6. Liability and changes
6.1 The service is provided on an "as is" basis. Codevertex's aggregate liability is limited to
the fees paid in the three months preceding a claim.
6.2 Codevertex may update these terms; continued use after a new version takes effect constitutes
acceptance of the updated terms.

## 7. Termination
7.1 Either party may terminate with notice. On termination, access ends and data is handled per
clause 3 and applicable law.

By ticking "I accept", you confirm you have read and agree to these terms.`
