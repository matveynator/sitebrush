// Package stripeconnect contains payment calculation and verification primitives.
// A Stripe payment becomes paid only after a verified, idempotently processed
// webhook; returning from Checkout is never evidence of settlement.
package stripeconnect

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type DestinationCharge struct {
	AmountMinor           int64
	ApplicationFeeMinor   int64
	ConnectedTransferMinor int64
	Currency              string
}

// SplitDestinationCharge calculates the platform application fee in minor
// units. Stripe processing fees are separate and must be reconciled from the
// Stripe balance transaction, not guessed by this calculation.
func SplitDestinationCharge(amountMinor int64, currency string, feeBPS int) (DestinationCharge, error) {
	currency = strings.ToLower(strings.TrimSpace(currency))
	if amountMinor <= 0 || feeBPS < 0 || feeBPS > 10000 || len(currency) != 3 {
		return DestinationCharge{}, errors.New("invalid amount, currency or platform fee")
	}
	for _, character := range currency {
		if character < 'a' || character > 'z' {
			return DestinationCharge{}, errors.New("currency must be an ISO currency code")
		}
	}
	// Avoid overflow in amountMinor*feeBPS, rounding half up in minor units.
	whole := (amountMinor / 10000) * int64(feeBPS)
	part := (amountMinor%10000*int64(feeBPS) + 5000) / 10000
	if whole > math.MaxInt64-part {
		return DestinationCharge{}, errors.New("platform fee overflows")
	}
	fee := whole + part
	if fee > amountMinor {
		fee = amountMinor
	}
	return DestinationCharge{AmountMinor: amountMinor, ApplicationFeeMinor: fee, ConnectedTransferMinor: amountMinor - fee, Currency: currency}, nil
}

type Checkout struct {
	InvoiceNumber      string
	CustomerEmail      string
	ConnectedAccountID string
	AmountMinor        int64
	Currency           string
	ApplicationFeeBPS  int
	SuccessURL         string
	CancelURL          string
}

// CheckoutForm produces the parameters for a server-side Stripe Checkout
// Session with a destination charge. It does not call Stripe or accept
// client-provided amounts. All values must originate from a frozen invoice.
func CheckoutForm(checkout Checkout) (url.Values, error) {
	if strings.TrimSpace(checkout.InvoiceNumber) == "" || !strings.Contains(checkout.CustomerEmail, "@") {
		return nil, errors.New("invoice number and customer email are required")
	}
	account := strings.TrimSpace(checkout.ConnectedAccountID)
	if !strings.HasPrefix(account, "acct_") || len(account) <= 5 {
		return nil, errors.New("verified connected account is required")
	}
	for _, c := range account[5:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return nil, errors.New("invalid connected account id")
		}
	}
	for _, target := range []string{checkout.SuccessURL, checkout.CancelURL} {
		u, err := url.Parse(target)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return nil, errors.New("checkout return URLs must use HTTPS")
		}
	}
	split, err := SplitDestinationCharge(checkout.AmountMinor, checkout.Currency, checkout.ApplicationFeeBPS)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"mode": {"payment"},
		"client_reference_id": {checkout.InvoiceNumber},
		"customer_email": {checkout.CustomerEmail},
		"success_url": {checkout.SuccessURL},
		"cancel_url": {checkout.CancelURL},
		"line_items[0][price_data][currency]": {split.Currency},
		"line_items[0][price_data][unit_amount]": {strconv.FormatInt(checkout.AmountMinor, 10)},
		"line_items[0][price_data][product_data][name]": {"Invoice " + checkout.InvoiceNumber},
		"line_items[0][quantity]": {"1"},
		"payment_intent_data[transfer_data][destination]": {account},
		"payment_intent_data[application_fee_amount]": {strconv.FormatInt(split.ApplicationFeeMinor, 10)},
		"payment_intent_data[metadata][invoice_number]": {checkout.InvoiceNumber},
	}
	return form, nil
}

// VerifyWebhookSignature checks the Stripe-Signature header against the exact
// raw request body, using the endpoint-specific whsec_ secret. Callers must
// still deduplicate Stripe event IDs inside the same DB transaction as payment
// application and verify account, currency, invoice and settled amount.
func VerifyWebhookSignature(header string, payload []byte, secret string, now time.Time) error {
	if secret == "" || len(payload) == 0 || len(header) > 8192 {
		return errors.New("invalid Stripe webhook signature inputs")
	}
	var timestamp string
	var signatures []string
	for _, field := range strings.Split(header, ",") {
		parts := strings.SplitN(strings.TrimSpace(field), "=", 2)
		if len(parts) != 2 { continue }
		if parts[0] == "t" { timestamp = parts[1] }
		if parts[0] == "v1" { signatures = append(signatures, parts[1]) }
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || len(signatures) == 0 {
		return errors.New("missing Stripe webhook signature or timestamp")
	}
	issuedAt := time.Unix(seconds, 0)
	delta := now.Sub(issuedAt)
	if delta < -5*time.Minute || delta > 5*time.Minute {
		return errors.New("Stripe webhook timestamp is outside the allowed window")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%s.", timestamp)
	_, _ = mac.Write(payload)
	want := mac.Sum(nil)
	valid := false
	for _, signature := range signatures {
		raw, decodeErr := hex.DecodeString(signature)
		if decodeErr == nil && hmac.Equal(raw, want) {
			valid = true
		}
	}
	if !valid {
		return errors.New("Stripe webhook signature mismatch")
	}
	return nil
}
