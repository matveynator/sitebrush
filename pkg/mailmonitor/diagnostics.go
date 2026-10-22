package mailmonitor

import (
	"encoding/json"
	"strings"
)

// Structured details fit the existing journal storage and synchronization
// envelope. They do not change the mail relay or NetChan protocols.
type Diagnostic struct {
	Transport   string `json:"transport,omitempty"`
	ClientIP    string `json:"client_ip,omitempty"`
	SMTPServer  string `json:"smtp_server,omitempty"`
	SMTPReply   string `json:"smtp_reply,omitempty"`
	SMTPCode    string `json:"smtp_code,omitempty"`
	Failure     string `json:"failure,omitempty"`
	Identifiers string `json:"diagnostic,omitempty"`
}

func (diagnostic Diagnostic) String() string {
	diagnostic.Transport = boundedField(diagnostic.Transport, 64)
	diagnostic.ClientIP = boundedField(diagnostic.ClientIP, 45)
	diagnostic.SMTPServer = boundedField(diagnostic.SMTPServer, 253)
	diagnostic.SMTPCode = boundedField(diagnostic.SMTPCode, 3)
	diagnostic.SMTPReply = boundedField(diagnostic.SMTPReply, 64)
	diagnostic.Failure = boundedField(diagnostic.Failure, 64)
	if diagnostic.SMTPReply != "" {
		diagnostic.Failure = ""
	}
	payload, _ := json.Marshal(diagnostic)
	if len(payload) > 500 {
		diagnostic.Identifiers = ""
		payload, _ = json.Marshal(diagnostic)
	}
	if len(payload) > 500 {
		diagnostic.SMTPReply = boundedField(diagnostic.SMTPReply, 32)
		diagnostic.Failure = boundedField(diagnostic.Failure, 32)
		payload, _ = json.Marshal(diagnostic)
	}
	return string(payload)
}

func (event Event) Diagnostic() Diagnostic {
	var diagnostic Diagnostic
	if json.Unmarshal([]byte(event.Detail), &diagnostic) == nil {
		return diagnostic
	}
	if event.Detail == "transport=smtp" {
		return Diagnostic{Identifiers: event.Detail}
	}
	// Older rows retain their audit details, while the main journal only shows
	// transport information that those rows actually recorded.
	for _, field := range strings.Fields(event.Detail) {
		if strings.HasPrefix(field, "transport=netchan") {
			diagnostic.Transport = strings.TrimPrefix(field, "transport=")
			diagnostic.Identifiers = event.Detail
		} else if field == "transport=https" {
			diagnostic.Transport = "https"
			diagnostic.Identifiers = event.Detail
		}
	}
	if event.Detail != "" && diagnostic.Transport == "" && (event.Stage == "failed" || event.Stage == "retry" || strings.HasSuffix(event.Stage, "_failed")) {
		diagnostic.Failure = "Delivery failed"
	}
	return diagnostic
}

func (event Event) DisplayStage() string {
	if event.Stage == "transport_sent" {
		return "sent"
	}
	if event.Stage == "accepted" && event.AcceptedBy == "central_relay" {
		return "sent"
	}
	if strings.HasPrefix(event.SMTPCode, "5") {
		return "rejected"
	}
	if event.Stage == "retry" || strings.HasPrefix(event.SMTPCode, "4") {
		return "temporary_failure"
	}
	return event.Stage
}
