package mailout

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
)

func TestDeliveryWorkerDoesNotLogSecretErrorDetails(t *testing.T) {
	var capturedLogs bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&capturedLogs)
	defer log.SetOutput(originalWriter)

	jobs := make(chan DeliveryJob, 1)
	workerDone := make(chan struct{})
	go func() {
		runDeliveryWorker(context.Background(), jobs, func(context.Context, Message) error {
			return errors.New("SMTP rejected login code SUPER_SECRET_MARKER")
		})
		close(workerDone)
	}()

	jobs <- DeliveryJob{Message: Message{
		To:      "owner@example.com",
		Subject: "login code SUPER_SECRET_MARKER",
		Body:    "SUPER_SECRET_MARKER",
	}}
	close(jobs)
	<-workerDone

	logOutput := capturedLogs.String()
	if !strings.Contains(logOutput, "email delivery failed") {
		t.Fatal("delivery worker did not report the failed delivery")
	}
	if strings.Contains(logOutput, "SUPER_SECRET_MARKER") {
		t.Fatalf("SECURITY: delivery log leaked secret error details: %q", logOutput)
	}
}
