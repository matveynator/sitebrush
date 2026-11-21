package stripeconnect

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"testing"
	"time"
)

func TestDestinationChargeFeesAndConservation(t *testing.T) {
	for _, testcase := range []struct{ amount int64; bps int; fee int64 }{
		{10000, 200, 200}, {101, 200, 2}, {1, 200, 0},
		{1, 7500, 1}, {12345, 0, 0}, {12345, 10000, 12345},
		{math.MaxInt64, 500, 461168601842738790},
	} {
		split, err := SplitDestinationCharge(testcase.amount, "EUR", testcase.bps)
		if err != nil { t.Fatal(err) }
		if split.ApplicationFeeMinor != testcase.fee { t.Errorf("amount %d fee %d, want %d", testcase.amount, split.ApplicationFeeMinor, testcase.fee) }
		if split.ApplicationFeeMinor+split.ConnectedTransferMinor != split.AmountMinor { t.Fatal("split loses minor units") }
	}
	for _, testcase := range []struct{ amount int64; currency string; bps int }{
		{0, "EUR", 200}, {100, "EURO", 200}, {100, "EUR", -1}, {100, "EUR", 10001},
	} {
		if _, err := SplitDestinationCharge(testcase.amount, testcase.currency, testcase.bps); err == nil { t.Errorf("accepted invalid fee: %+v", testcase) }
	}
}

func TestCheckoutFormIsBoundToInvoiceAndMerchant(t *testing.T) {
	cfg := Checkout{
		InvoiceNumber: "SB-20261121-1", CustomerEmail: "buyer@example.com",
		ConnectedAccountID: "acct_123456789", AmountMinor: 10000,
		Currency: "EUR", ApplicationFeeBPS: 200,
		SuccessURL: "https://sitebrush.com/pay/success", CancelURL: "https://sitebrush.com/pay/cancel",
	}
	form, err := CheckoutForm(cfg)
	if err != nil { t.Fatal(err) }
	if form.Get("payment_intent_data[application_fee_amount]") != "200" ||
		form.Get("payment_intent_data[transfer_data][destination]") != cfg.ConnectedAccountID ||
		form.Get("line_items[0][price_data][unit_amount]") != "10000" ||
		form.Get("client_reference_id") != cfg.InvoiceNumber {
		t.Fatalf("incorrect Stripe Checkout contract: %v", form)
	}
	cfg.SuccessURL = "http://evil.example"
	if _, err := CheckoutForm(cfg); err == nil { t.Fatal("insecure callback accepted") }
	cfg.SuccessURL = "https://sitebrush.com/pay/success"
	cfg.ConnectedAccountID = "acct_../../bad"
	if _, err := CheckoutForm(cfg); err == nil { t.Fatal("invalid recipient account accepted") }
}

func TestWebhookRawBodySignatureAndReplayWindow(t *testing.T) {
	secret := "whsec_testing_not_a_real_key"
	now := time.Date(2026, 11, 21, 20, 0, 0, 0, time.UTC)
	body := []byte(`{"id":"evt_one","type":"checkout.session.completed"}`)
	timestamp := fmt.Sprint(now.Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%s.", timestamp)
	_, _ = mac.Write(body)
	header := "t=" + timestamp + ",v1=wrong,v1=" + hex.EncodeToString(mac.Sum(nil))
	if err := VerifyWebhookSignature(header, body, secret, now); err != nil { t.Fatal(err) }
	if err := VerifyWebhookSignature(header, append([]byte(nil), body[:len(body)-1]...), secret, now); err == nil { t.Fatal("tampered payload accepted") }
	if err := VerifyWebhookSignature(header, body, secret, now.Add(6*time.Minute)); err == nil { t.Fatal("stale webhook accepted") }
	if err := VerifyWebhookSignature(header, body, secret, now.Add(-6*time.Minute)); err == nil { t.Fatal("future webhook accepted") }
	if err := VerifyWebhookSignature(header, body, "another-secret", now); err == nil { t.Fatal("wrong secret accepted") }
	if err := VerifyWebhookSignature("t=wrong,v1=aa", body, secret, now); err == nil { t.Fatal("invalid timestamp accepted") }
}
