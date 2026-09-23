package mailout

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"sort"
	"strings"
	"time"
)

type Message struct {
	MessageID string
	Kind      string
	From      string
	To        string
	Subject   string
	Body      string
	HTMLBody  string
}

const DeliveryQueueSize = 256

type Sender func(context.Context, Message) error

type DeliveryJob struct {
	Message Message
}

func StartDeliveryWorker(ctx context.Context, sender Sender) chan DeliveryJob {
	jobs := make(chan DeliveryJob, DeliveryQueueSize)
	go runDeliveryWorker(ctx, jobs, sender)
	return jobs
}

type DirectSender struct {
	Hostname    string
	DialTimeout time.Duration
	dialContext func(context.Context, string, string) (net.Conn, error)
}

func (sender DirectSender) Send(ctx context.Context, message Message) error {
	fromAddress, err := parseSingleAddress(message.From)
	if err != nil {
		return fmt.Errorf("from address: %w", err)
	}
	toAddress, err := parseSingleAddress(message.To)
	if err != nil {
		return fmt.Errorf("recipient address: %w", err)
	}
	recipientDomain := addressDomain(toAddress)
	if recipientDomain == "" {
		return errors.New("recipient address has no domain")
	}
	targetHosts, err := lookupMailHosts(ctx, recipientDomain)
	if err != nil {
		return err
	}
	payload := buildMessagePayloadWithID(fromAddress, toAddress, message.Subject, message.Body, message.HTMLBody, message.MessageID)
	var lastErr error
	for _, targetHost := range targetHosts {
		if err := sender.sendToHost(ctx, targetHost, fromAddress.Address, toAddress.Address, payload); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("no mail hosts found")
}

func runDeliveryWorker(ctx context.Context, jobs <-chan DeliveryJob, sender Sender) {
	if sender == nil {
		sender = DirectSender{}.Send
	}
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-jobs:
			sendCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			if err := sender(sendCtx, job.Message); err != nil {
				log.Printf("email delivery failed error_type=%T", err)
			}
			cancel()
		}
	}
}

type startTLSError struct{ cause error }

func (err startTLSError) Error() string { return "starttls: " + err.cause.Error() }
func (err startTLSError) Unwrap() error { return err.cause }

func (sender DirectSender) sendToHost(ctx context.Context, targetHost, fromAddress, toAddress string, payload []byte) error {
	return sender.sendToEndpoint(ctx, targetHost, net.JoinHostPort(targetHost, "25"), fromAddress, toAddress, payload)
}

func (sender DirectSender) sendToEndpoint(ctx context.Context, targetHost, endpoint, fromAddress, toAddress string, payload []byte) error {
	// MX delivery is opportunistic: prefer authenticated encryption, then legacy
	// encryption, then plaintext. Never retry a submitted message as a TLS fallback.
	for _, mode := range []string{"verified", "legacy", "plaintext"} {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := sender.sendSMTP(ctx, targetHost, endpoint, fromAddress, toAddress, payload, mode)
		var negotiationError startTLSError
		if !errors.As(err, &negotiationError) || mode == "plaintext" {
			return err
		}
		log.Printf("MAIL TLS negotiation failed host=%q mode=%s fallback=true error_type=%T", targetHost, mode, err)
	}
	return errors.New("SMTP transport attempts exhausted")
}

func legacySMTPConfig(targetHost string) *tls.Config {
	// Compatibility applies only to unauthenticated outbound MX transport, never
	// to HTTPS, SMTP AUTH, or the connection between SiteBrush and its relay.
	config := &tls.Config{ServerName: targetHost, MinVersion: tls.VersionTLS10, InsecureSkipVerify: true}
	for _, suite := range tls.CipherSuites() {
		config.CipherSuites = append(config.CipherSuites, suite.ID)
	}
	config.CipherSuites = append(config.CipherSuites,
		tls.TLS_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_RSA_WITH_AES_128_CBC_SHA, tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA, tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA)
	return config
}

func (sender DirectSender) sendSMTP(ctx context.Context, targetHost, endpoint, fromAddress, toAddress string, payload []byte, mode string) error {
	timeout := sender.DialTimeout
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	hostName := strings.Trim(strings.TrimSpace(sender.Hostname), ".")
	if hostName == "" {
		hostName = "sitebrush.local"
	}
	dialer := &net.Dialer{Timeout: timeout}
	dial := sender.dialContext
	if dial == nil {
		dial = dialer.DialContext
	}
	connection, err := dial(ctx, "tcp", endpoint)
	if err != nil {
		return fmt.Errorf("%s: %w", targetHost, err)
	}
	defer connection.Close()
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-finished:
		}
	}()
	client, err := smtp.NewClient(connection, targetHost)
	if err != nil {
		return fmt.Errorf("%s: %w", targetHost, err)
	}
	defer client.Close()
	if err := client.Hello(hostName); err != nil {
		return fmt.Errorf("%s hello: %w", targetHost, err)
	}
	if ok, _ := client.Extension("STARTTLS"); ok && mode != "plaintext" {
		tlsConfig := &tls.Config{ServerName: targetHost, MinVersion: tls.VersionTLS12}
		if mode == "legacy" {
			tlsConfig = legacySMTPConfig(targetHost)
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("%s: %w", targetHost, startTLSError{cause: err})
		}
	} else {
		log.Printf("MAIL transport host=%q mode=plaintext encrypted=false", targetHost)
	}
	if state, encrypted := client.TLSConnectionState(); encrypted {
		log.Printf("MAIL transport host=%q mode=%s tls_version=%x certificate_verified=%t", targetHost, mode, state.Version, len(state.VerifiedChains) > 0)
	}
	if err := client.Mail(fromAddress); err != nil {
		return fmt.Errorf("%s mail from: %w", targetHost, err)
	}
	if err := client.Rcpt(toAddress); err != nil {
		return fmt.Errorf("%s rcpt to: %w", targetHost, err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("%s data: %w", targetHost, err)
	}
	if _, err := writer.Write(payload); err != nil {
		_ = writer.Close()
		return fmt.Errorf("%s write: %w", targetHost, err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("%s close data: %w", targetHost, err)
	}
	// DATA acceptance is final; a broken goodbye must not queue a duplicate code.
	if err := client.Quit(); err != nil {
		log.Printf("MAIL accepted host=%q quit_error_type=%T", targetHost, err)
	}
	return nil
}

func parseSingleAddress(rawAddress string) (*mail.Address, error) {
	address, err := mail.ParseAddress(strings.TrimSpace(rawAddress))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(address.Address) == "" {
		return nil, errors.New("empty address")
	}
	return address, nil
}

func addressDomain(address *mail.Address) string {
	if address == nil {
		return ""
	}
	atIndex := strings.LastIndex(address.Address, "@")
	if atIndex < 0 || atIndex == len(address.Address)-1 {
		return ""
	}
	return strings.ToLower(strings.Trim(address.Address[atIndex+1:], ". "))
}

func lookupMailHosts(ctx context.Context, domain string) ([]string, error) {
	mxRecords, err := net.DefaultResolver.LookupMX(ctx, domain)
	if err == nil && len(mxRecords) > 0 {
		sort.Slice(mxRecords, func(left, right int) bool {
			return mxRecords[left].Pref < mxRecords[right].Pref
		})
		hosts := make([]string, 0, len(mxRecords))
		for _, mxRecord := range mxRecords {
			host := strings.Trim(strings.TrimSpace(mxRecord.Host), ".")
			if host != "" {
				hosts = append(hosts, host)
			}
		}
		if len(hosts) > 0 {
			return hosts, nil
		}
	}
	if err != nil {
		var dnsErr *net.DNSError
		if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
			return nil, fmt.Errorf("lookup MX for %s: %w", domain, err)
		}
	}
	return []string{domain}, nil
}

func buildMessagePayload(fromAddress, toAddress *mail.Address, subject, body string, htmlBody ...string) []byte {
	selectedHTMLBody := ""
	if len(htmlBody) > 0 {
		selectedHTMLBody = htmlBody[0]
	}
	return buildMessagePayloadWithID(fromAddress, toAddress, subject, body, selectedHTMLBody, "")
}

func buildMessagePayloadWithID(fromAddress, toAddress *mail.Address, subject, body, htmlBody, stableMessageID string) []byte {
	headers := []string{
		"From: " + fromAddress.String(),
		"To: " + toAddress.String(),
		"Subject: " + mime.QEncoding.Encode("utf-8", strings.TrimSpace(subject)),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"Message-ID: " + messageIDForDelivery(fromAddress, stableMessageID),
		"MIME-Version: 1.0",
	}
	selectedHTMLBody := strings.TrimSpace(htmlBody)
	if selectedHTMLBody == "" {
		headers = append(headers, "Content-Type: text/plain; charset=utf-8", "Content-Transfer-Encoding: 8bit")
		return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + strings.TrimRight(body, "\r\n") + "\r\n")
	}
	boundary := "sitebrush-" + strings.Trim(messageID(fromAddress), "<>")
	headers = append(headers, `Content-Type: multipart/alternative; boundary="`+boundary+`"`)
	parts := []string{
		"--" + boundary + "\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + strings.TrimRight(body, "\r\n") + "\r\n",
		"--" + boundary + "\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + strings.TrimRight(selectedHTMLBody, "\r\n") + "\r\n",
		"--" + boundary + "--\r\n",
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + strings.Join(parts, ""))
}

func messageID(fromAddress *mail.Address) string {
	var randomBytes [16]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return fmt.Sprintf("<%d.%d@%s>", time.Now().UnixNano(), time.Now().Unix(), messageIDDomain(fromAddress))
	}
	return "<" + hex.EncodeToString(randomBytes[:]) + "@" + messageIDDomain(fromAddress) + ">"
}

func messageIDForDelivery(fromAddress *mail.Address, stableMessageID string) string {
	stableMessageID = strings.TrimSpace(stableMessageID)
	if stableMessageID == "" {
		return messageID(fromAddress)
	}
	for _, character := range stableMessageID {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '.' && character != '-' && character != '_' {
			return messageID(fromAddress)
		}
	}
	return "<" + stableMessageID + "@" + messageIDDomain(fromAddress) + ">"
}

func messageIDDomain(fromAddress *mail.Address) string {
	domain := addressDomain(fromAddress)
	if domain == "" || domain == "localhost" {
		return "sitebrush.local"
	}
	return domain
}
