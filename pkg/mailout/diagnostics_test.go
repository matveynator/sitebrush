package mailout

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPDiagnosticsFinalReplyAndSecretIsolation(t *testing.T) {
	for _, scenario := range []struct{ name, rejectAt, reply, outcome, stage, code, enhanced string }{
		{"accepted", "", "250 2.0.0 password hunter2 otp 1234 https://site/action/private", "accepted_for_delivery", "data_reply", "250", "2.0.0"},
		{"temporary recipient", "RCPT", "450 4.2.0 password hunter2", "temporary_rejection", "rcpt_to", "450", "4.2.0"},
		{"permanent sender", "MAIL", "550 5.7.26 otp 1234", "permanent_rejection", "mail_from", "550", "5.7.26"},
		{"rejected DATA", "DATA", "554 5.7.1 password hunter2", "permanent_rejection", "data", "554", "5.7.1"},
		{"rejected body", "BODY", "451 4.3.0 https://site/action/private", "temporary_rejection", "data_reply", "451", "4.3.0"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			connections := make(chan net.Conn)
			sender := pipeSMTPSender(connections)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				conn := <-connections
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				reader := bufio.NewReader(conn)
				fmt.Fprint(conn, "220 test\r\n")
				for {
					command, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					stage := strings.Fields(command)[0]
					if scenario.rejectAt == stage {
						fmt.Fprint(conn, scenario.reply+"\r\n")
						return
					}
					switch stage {
					case "EHLO", "HELO", "MAIL", "RCPT":
						fmt.Fprint(conn, "250 OK\r\n")
					case "DATA":
						fmt.Fprint(conn, "354 continue\r\n")
						for {
							line, err := reader.ReadString('\n')
							if err != nil {
								return
							}
							if line == ".\r\n" {
								break
							}
						}
						fmt.Fprint(conn, scenario.reply+"\r\n")
						if scenario.rejectAt == "BODY" {
							return
						}
					case "QUIT":
						fmt.Fprint(conn, "221 bye\r\n")
						return
					}
				}
			}()
			var events []SMTPDiagnostic
			ctx := WithSMTPDiagnostics(context.Background(), func(d SMTPDiagnostic) { events = append(events, d) })
			err := sender.sendToEndpoint(ctx, "mx.example", "mx.example:25", "sender@example", "recipient@example", []byte("Subject: password hunter2\r\n\r\notp 1234\r\n"))
			<-finished
			if (err != nil) != (scenario.rejectAt != "") {
				t.Fatalf("unexpected result: %v", err)
			}
			if want := strings.HasPrefix(scenario.code, "5"); IsPermanentFailure(err) != want {
				t.Fatalf("permanent failure classification: err=%v want=%t", err, want)
			}
			last := events[len(events)-1]
			if last.Outcome != scenario.outcome || last.Stage != scenario.stage || last.Code != scenario.code || last.EnhancedCode != scenario.enhanced || last.TLSMode != "plaintext" {
				t.Fatalf("wrong diagnostic: %+v", last)
			}
			diagnostic := fmt.Sprintf("%+v %v", events, err)
			for _, secret := range []string{"hunter2", "1234", "https://", "Subject:", "recipient@"} {
				if strings.Contains(diagnostic, secret) {
					t.Fatalf("diagnostics exposed %q", secret)
				}
			}
		})
	}
}
