package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/securitysync"
)

func TestSecurityBoundarySecuritySignalCannotForgeQuorumWithEmbeddedInstallationIDs(t *testing.T) {
	application, _ := newTestApplication(t)
	stop := make(chan struct{})
	defer close(stop)

	reputation, err := securitysync.Start("", stop)
	if err != nil {
		t.Fatal(err)
	}
	application.securityReputation = reputation

	baseRequest := signedServiceMailRequestForTest(t, application, serviceMailRequest{
		Version:      1,
		SourceDomain: "example.com",
		CodeKind:     "security_signal",
		LanguageCode: "en",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
		SecuritySignal: &securitysync.Signal{
			IP:         "8.8.8.8",
			Category:   "injection",
			ObservedAt: time.Now().UTC(),
		},
	})
	registerServiceMailInstallationForTest(t, application, baseRequest)

	for attempt := 0; attempt < 3; attempt++ {
		request := signedServiceMailRequestForTest(t, application, serviceMailRequest{
			Version:      1,
			SourceDomain: "example.com",
			CodeKind:     "security_signal",
			LanguageCode: "en",
			CreatedAt:    time.Now().UTC().Format(time.RFC3339),
			SecuritySignal: &securitysync.Signal{
				IP:             "8.8.8.8",
				Category:       "injection",
				Description:    "forged embedded installation identity",
				InstallationID: fmt.Sprintf("forged-installation-%d", attempt),
				ObservedAt:     time.Now().UTC(),
			},
		})
		status, statusCode := application.handleSecuritySignalRequest(context.Background(), request)
		if statusCode != http.StatusOK {
			t.Fatalf("signal %d status=%d %q", attempt, statusCode, status)
		}
	}

	reply := make(chan securitysync.Result, 1)
	reputation <- securitysync.Request{Query: true, Reply: reply}
	result := <-reply
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if len(result.Entries) != 0 {
		t.Fatalf("SECURITY: one authenticated installation forged reputation quorum: %#v", result.Entries)
	}
}
