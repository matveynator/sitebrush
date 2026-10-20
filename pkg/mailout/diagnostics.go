package mailout

import (
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"strconv"
	"strings"
)

// Reports belong to the delivery consumer. Only protocol metadata and recognized
// reply summaries cross this boundary; an SMTP peer can reflect message secrets.
type SMTPReport struct {
	Server  string `json:"smtp_server,omitempty"`
	Code    string `json:"smtp_code,omitempty"`
	Reply   string `json:"smtp_reply,omitempty"`
	Failure string `json:"failure,omitempty"`
}

func (report SMTPReport) publish(replies chan<- SMTPReport) {
	if replies == nil {
		return
	}
	defer func() { _ = recover() }()
	select {
	case replies <- report:
	default:
	}
}

func smtpServer(connection net.Conn, host string) string {
	if address, ok := connection.RemoteAddr().(*net.TCPAddr); ok && address.Port > 0 {
		return net.JoinHostPort(host, strconv.Itoa(address.Port))
	}
	return host
}

func smtpReply(code int, reply string) string {
	// Never retain arbitrary peer prose: even an apparently harmless response can
	// contain passwords, short codes, credentials or an echoed message body.
	normalized := strings.ToLower(strings.TrimSpace(reply))
	if fields := strings.Fields(normalized); len(fields) > 1 && strings.Count(fields[0], ".") == 2 {
		switch fields[0] {
		case "5.1.1":
			return "5.1.1 · User unknown"
		case "4.2.2", "5.2.2":
			return fields[0] + " · Mailbox full"
		case "5.7.1":
			return "5.7.1 · Delivery not authorized"
		case "5.7.26":
			return "5.7.26 · Sender unauthenticated"
		}
		normalized = strings.Join(fields[1:], " ")
	}
	switch normalized {
	case "ok", "accepted", "message accepted", "message accepted for delivery", "queued", "queued for delivery":
		return "OK"
	case "user unknown", "unknown user", "unknown recipient", "no such user", "mailbox unavailable", "recipient not found":
		return "User unknown"
	case "try again later", "please try again later", "temporarily unavailable", "temporary failure":
		return "Try again later"
	case "relay access denied", "relaying denied", "relay denied":
		return "Relay denied"
	case "mailbox full", "quota exceeded":
		return "Mailbox full"
	case "authentication required", "authentication failed":
		return "Authentication failed"
	}
	// These summaries are derived from protocol codes, never invented peer text.
	switch code {
	case 250:
		return "Message accepted"
	case 421, 450, 451, 452:
		return "Temporary SMTP failure"
	case 550, 551, 552, 553, 554:
		return "Message rejected"
	default:
		return "SMTP response"
	}
}

type smtpDeliveryError struct {
	report SMTPReport
	cause  error
}

func (err smtpDeliveryError) Error() string {
	return fmt.Sprintf("SMTP %s %s %s", err.report.Server, err.report.Code, firstDiagnostic(err.report.Reply, err.report.Failure))
}

func (err smtpDeliveryError) Unwrap() error { return err.cause }

func firstDiagnostic(reply, failure string) string {
	if reply != "" {
		return reply
	}
	return failure
}

func smtpFailure(report SMTPReport, err error) SMTPReport {
	var response *textproto.Error
	if errors.As(err, &response) {
		report.Code = strconv.Itoa(response.Code)
		report.Reply = smtpReply(response.Code, response.Msg)
		report.Failure = ""
	}
	return report
}

func SMTPFailure(err error) SMTPReport {
	return smtpFailure(SMTPReport{}, err)
}
