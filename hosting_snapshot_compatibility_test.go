package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/matveynator/sitebrush/v2/pkg/hostingandsupport"
)

func TestHostingSnapshotSchemaCompatibilityPreservesSignedTotals(t *testing.T) {
	for _, olderPeer := range []bool{false, true} {
		application, _ := newTestApplication(t)
		snapshot := hostingandsupport.HostingSnapshot{Version: 4, Sites: []hostingandsupport.HostingSnapshotSite{{Domain: "example.org", UsedBytes: 1000, AnalyticsBytes: 400}}}
		attempts := 0
		request := serviceMailRequest{CodeKind: "hosting_snapshot", HostingSnapshot: &snapshot}
		err := application.sendSignedRequestCompatible(context.Background(), request, func(payload []byte) error {
			attempts++
			var incoming serviceMailRequest
			if err := json.Unmarshal(payload, &incoming); err != nil {
				return err
			}
			if olderPeer {
				incoming.HostingSnapshot.Sites[0].AnalyticsBytes = 0
			}
			if verifyServiceMailRequestSignature(incoming) != nil {
				return serviceMailRelayRejection{Transport: "netchan", StatusCode: http.StatusForbidden, Message: "signature verification failed"}
			}
			if incoming.HostingSnapshot.Sites[0].UsedBytes != 1000 {
				t.Fatal("compatibility discarded occupied disk")
			}
			return nil
		})
		expectedAttempts := 1
		if olderPeer {
			expectedAttempts = 2
		}
		if err != nil || attempts != expectedAttempts || snapshot.Sites[0].AnalyticsBytes != 400 {
			t.Fatal("compatibility failed or changed caller state", err, attempts, snapshot)
		}
	}
}

func TestHostingSnapshotCompatibilityStillRejectsTampering(t *testing.T) {
	application, _ := newTestApplication(t)
	request := serviceMailRequest{CodeKind: "hosting_snapshot", HostingSnapshot: &hostingandsupport.HostingSnapshot{Sites: []hostingandsupport.HostingSnapshotSite{{UsedBytes: 1000, AnalyticsBytes: 400}}}}
	attempts := 0
	err := application.sendSignedRequestCompatible(context.Background(), request, func(payload []byte) error {
		attempts++
		var incoming serviceMailRequest
		if err := json.Unmarshal(payload, &incoming); err != nil {
			return err
		}
		incoming.HostingSnapshot.Sites[0].UsedBytes++
		if verifyServiceMailRequestSignature(incoming) == nil {
			t.Fatal("tampered total accepted")
		}
		return serviceMailRelayRejection{StatusCode: http.StatusForbidden, Message: "signature verification failed"}
	})
	var rejection serviceMailRelayRejection
	if attempts != 2 || !errors.As(err, &rejection) {
		t.Fatal("tampered payload not rejected after bounded retry", attempts, err)
	}
}
