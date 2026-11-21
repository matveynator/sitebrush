// Package billingnetchan delivers durable payment facts across authenticated
// SiteBrush NetChan connections. Stripe remains the external payment authority.
package billingnetchan

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/matveynator/netchan/v2"
)

const SchemaVersion = 1

// Event is an immutable snapshot of a payment already verified and committed
// by the central billing owner. It intentionally contains no card data, tokens
// or payment-provider secrets.
type Event struct {
	SchemaVersion   int
	EventID         string
	InstallationID  string
	InvoiceNumber   string
	PaymentID       string
	Currency        string
	AmountMinor     int64
	Status          string // paid, refunded, disputed
	OccurredAt      string // RFC3339 UTC, from the payment authority
}

// Message is the per-delivery NetChan request. Reply carries the right to send
// exactly one result; Session is closed by the caller when it abandons a request.
type Message struct {
	Delivery Event
	Reply    chan<- Receipt
	Session  <-chan struct{}
}

type Receipt struct {
	EventID   string
	Committed bool // true only after the durable owner acknowledges the event
	Duplicate bool
	Error     string
}

func (event Event) Validate(installationID string) error {
	if event.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported payment protocol version")
	}
	if event.InstallationID == "" || event.InstallationID != installationID {
		return errors.New("payment installation mismatch")
	}
	if strings.TrimSpace(event.EventID) == "" || len(event.EventID) > 256 ||
		strings.TrimSpace(event.InvoiceNumber) == "" || len(event.InvoiceNumber) > 128 ||
		strings.TrimSpace(event.PaymentID) == "" || len(event.PaymentID) > 256 {
		return errors.New("payment identifiers are missing or too long")
	}
	if event.AmountMinor <= 0 || event.AmountMinor > 1<<52 {
		return errors.New("invalid payment amount")
	}
	if len(event.Currency) != 3 {
		return errors.New("invalid payment currency")
	}
	for _, character := range event.Currency {
		if character < 'A' || character > 'Z' {
			return errors.New("payment currency must be uppercase ISO code")
		}
	}
	switch event.Status {
	case "paid", "refunded", "disputed":
	default:
		return errors.New("unsupported payment event status")
	}
	if _, err := time.Parse(time.RFC3339, event.OccurredAt); err != nil || !strings.HasSuffix(event.OccurredAt, "Z") {
		return errors.New("invalid payment event UTC timestamp")
	}
	return nil
}

// Commit is the database owner's synchronous, atomic and idempotent operation.
// The callback must verify invoice/server/currency/amount, insert EventID once,
// and update the local projection in the same transaction. It returns duplicate
// when that exact event was previously committed without changing the invoice.
type Commit func(Event) (duplicate bool, err error)

// Apply refuses untrusted peer sessions before any state transition.
// authenticated must come from a verified NetChan mTLS/central identity,
// never from a field in the delivered message.
func Apply(authenticated bool, installationID string, event Event, commit Commit) Receipt {
	receipt := Receipt{EventID: event.EventID}
	if !authenticated {
		receipt.Error = "untrusted payment source"
		return receipt
	}
	if err := event.Validate(installationID); err != nil {
		receipt.Error = err.Error()
		return receipt
	}
	if commit == nil {
		receipt.Error = "payment journal unavailable"
		return receipt
	}
	duplicate, err := commit(event)
	if err != nil {
		receipt.Error = "payment journal commit failed"
		return receipt
	}
	receipt.Duplicate = duplicate
	receipt.Committed = true
	return receipt
}

// Serve processes one logical NetChan peer connection. The caller must have
// authenticated the central peer using the existing SiteBrush NetChan trust
// configuration before setting centralAuthenticated to true. It owns the
// connection and must close it only after this worker has exited.
func Serve(connection *netchan.Channel[Message], centralAuthenticated bool, installationID string, commit Commit, stop <-chan struct{}) {
	if connection == nil {
		return
	}
	for {
		select {
		case request, ok := <-connection.Receive:
			if !ok {
				return
			}
			if request.Reply == nil || request.Session == nil {
				// Malformed requests must not monopolize the payment owner.
				continue
			}
			select {
			case <-request.Session:
				continue
			default:
			}
			receipt := Apply(centralAuthenticated, installationID, request.Delivery, commit)
			select {
			case request.Reply <- receipt:
			case <-request.Session:
			case <-connection.Done:
				return
			case <-stop:
				return
			}
		case <-connection.Done:
			return
		case <-stop:
			return
		}
	}
}

// Deliver sends immutable payment facts to a remote installation over NetChan.
// A timeout or disconnected session is NOT proof the remote DB didn't commit.
// The central journal must retry with the same EventID; the receiving owner
// deduplicates it atomically and responds with the previous successful result.
func Deliver(connection *netchan.Channel[Message], event Event, stop <-chan struct{}, timeout time.Duration) (Receipt, error) {
	if connection == nil || timeout <= 0 {
		return Receipt{}, errors.New("payment channel unavailable")
	}
	if strings.TrimSpace(event.EventID) == "" {
		return Receipt{}, errors.New("event id required")
	}
	reply := make(chan Receipt)
	session := make(chan struct{})
	defer close(session)
	request := Message{Delivery: event, Reply: reply, Session: session}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case connection.Send <- request:
	case <-connection.Done:
		return Receipt{}, errors.New("payment channel disconnected")
	case <-stop:
		return Receipt{}, errors.New("payment delivery stopped")
	case <-timer.C:
		return Receipt{}, errors.New("payment delivery timed out")
	}
	select {
	case result := <-reply:
		if !result.Committed || result.EventID != event.EventID {
			return result, errors.New("payment acknowledgement not committed")
		}
		return result, nil
	case <-connection.Done:
		return Receipt{}, errors.New("payment channel disconnected before acknowledgement")
	case <-stop:
		return Receipt{}, errors.New("payment delivery stopped")
	case <-timer.C:
		return Receipt{}, errors.New("payment acknowledgement timed out")
	}
}
