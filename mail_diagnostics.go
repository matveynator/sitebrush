package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/mailmonitor"
	"github.com/matveynator/sitebrush/v2/pkg/mailout"
)

type mailAttemptContextKey struct{}
type mailReceiptContextKey struct{}
type mailReceiptTransport struct{ Transport, Protocol, Network string }

func (a *App) observeSMTP(ctx context.Context, message mailout.Message) context.Context {
	attempt, _ := ctx.Value(mailAttemptContextKey{}).(int)
	return mailout.WithSMTPDiagnostics(ctx, func(d mailout.SMTPDiagnostic) {
		stage := "smtp_result"
		if d.Outcome == "attempt" {
			stage = "smtp_attempt"
		}
		a.recordMailDiagnostic(ctx, mailmonitor.Event{
			ID: mailmonitor.NewID(), MessageID: message.MessageID, FlowID: message.MonitorFlowID,
			Kind: firstNonEmpty(message.Kind, "system"), Stage: stage, Domain: mailMonitorDomainForMessage(message),
			Recipient: message.To, Route: mailout.RouteLocal, Attempt: attempt, SMTPCode: d.Code,
			Detail: fmt.Sprintf("transport=smtp network=tcp mx=%s stage=%s tls=%s outcome=%s enhanced=%s", d.Host, d.Stage, d.TLSMode, d.Outcome, d.EnhancedCode),
		})
	})
}

// Called only after signature, installation and recipient authorization checks.
func (a *App) recordMailReceipt(ctx context.Context, request serviceMailRequest) {
	d, ok := ctx.Value(mailReceiptContextKey{}).(mailReceiptTransport)
	if !ok || strings.TrimSpace(request.MessageID) == "" {
		return
	}
	a.recordMailDiagnostic(ctx, mailmonitor.Event{
		ID: mailmonitor.NewID(), MessageID: request.MessageID, Kind: request.CodeKind, Stage: "transport_received",
		Domain: request.SourceDomain, Recipient: request.Recipient, Route: mailout.RouteRelay,
		Detail: fmt.Sprintf("direction=received transport=%s protocol=%s network=%s", d.Transport, d.Protocol, d.Network),
	})
}

func receivedMailProtocol(ctx context.Context) string {
	if d, ok := ctx.Value(mailReceiptContextKey{}).(mailReceiptTransport); ok {
		return d.Protocol
	}
	return "unknown"
}

// Only receiver-owned stream networks are translated into protocol labels.
func mailNetworkReceipt(network string) mailReceiptTransport {
	switch network {
	case "tcp", "tcp4", "tcp6":
		return mailReceiptTransport{"netchan", "tcp+tls", "tcp"}
	case "udp", "udp4", "udp6":
		return mailReceiptTransport{"netchan", "quic", "udp"}
	case "rfcomm", "ble":
		return mailReceiptTransport{"netchan", network, "bluetooth"}
	default:
		return mailReceiptTransport{"netchan", "unknown", "unknown"}
	}
}

// Optional diagnostics must not hold delivery workers behind a busy database.
func (a *App) recordMailDiagnostic(ctx context.Context, event mailmonitor.Event) {
	diagnosticContext, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	a.recordMailMonitorEvent(diagnosticContext, event)
}
