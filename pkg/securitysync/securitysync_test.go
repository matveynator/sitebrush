package securitysync

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReputationRequiresThreeIndependentInstallations(t *testing.T) {
	now := time.Now().UTC()
	records := map[string]map[string]evidenceSource{}
	for index, installationID := range []string{"one", "two"} {
		accepted, err := submit(records, Signal{
			IP:             "203.0.113.20",
			Category:       "injection",
			InstallationID: installationID,
			ObservedAt:     now.Add(time.Duration(index) * time.Second),
		}, now)
		if err != nil || !accepted {
			t.Fatalf("submit %s: accepted=%v err=%v", installationID, accepted, err)
		}
	}
	if entries := approved(records, now); len(entries) != 0 {
		t.Fatalf("two sources unexpectedly produced global reputation: %#v", entries)
	}
	accepted, err := submit(records, Signal{
		IP:             "203.0.113.20",
		Category:       "scanner-client",
		InstallationID: "three",
		ObservedAt:     now,
	}, now)
	if err != nil || !accepted {
		t.Fatalf("third submit: accepted=%v err=%v", accepted, err)
	}
	entries := approved(records, now)
	if len(entries) != 1 || entries[0].Confirmations != 3 {
		t.Fatalf("expected quorum entry, got %#v", entries)
	}
}

func TestStartAndRunLifecycle(t *testing.T) {
	stop := make(chan struct{})
	requests, err := Start("", stop)
	if err != nil || requests == nil {
		t.Fatalf("Start returned requests=%v err=%v", requests, err)
	}
	close(stop)
	badState := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(badState, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(badState, make(chan struct{})); err == nil {
		t.Fatal("Start accepted malformed state")
	}

	statePath := filepath.Join(t.TempDir(), "nested", "reputation.json")
	stop = make(chan struct{})
	requestsChannel := make(chan Request)
	done := make(chan struct{})
	go func() {
		run(statePath, stop, requestsChannel, map[string]map[string]evidenceSource{})
		close(done)
	}()
	reply := make(chan Result, 1)
	now := time.Now().UTC()
	requestsChannel <- Request{Signal: &Signal{IP: "203.0.113.77", Category: "injection", InstallationID: "one", ObservedAt: now}, Query: true, Reply: reply}
	result := <-reply
	if !result.Accepted || len(result.Entries) != 0 {
		t.Fatalf("signal result=%+v", result)
	}
	close(stop)
	<-done
	if encoded, err := os.ReadFile(statePath); err != nil || !strings.Contains(string(encoded), "203.0.113.77") {
		t.Fatalf("saved reputation state=%q err=%v", encoded, err)
	}
}

func TestReputationDoesNotCountOneInstallationMultipleTimes(t *testing.T) {
	now := time.Now().UTC()
	records := map[string]map[string]evidenceSource{}
	for index := 0; index < 5; index++ {
		accepted, err := submit(records, Signal{
			IP:             "198.51.100.15",
			Category:       "traversal",
			InstallationID: "same-installation",
			ObservedAt:     now.Add(time.Duration(index) * time.Second),
		}, now.Add(time.Duration(index)*time.Second))
		if err != nil || !accepted {
			t.Fatalf("submit %d: accepted=%v err=%v", index, accepted, err)
		}
	}
	if entries := approved(records, now.Add(10*time.Second)); len(entries) != 0 {
		t.Fatalf("one installation poisoned quorum: %#v", entries)
	}
}

func TestReputationRejectsPrivateAddressesAndWeakCategories(t *testing.T) {
	now := time.Now().UTC()
	records := map[string]map[string]evidenceSource{}
	tests := []Signal{
		{IP: "127.0.0.1", Category: "injection", InstallationID: "one", ObservedAt: now},
		{IP: "10.0.0.1", Category: "injection", InstallationID: "one", ObservedAt: now},
		{IP: "203.0.113.8", Category: "rapid-crawl", InstallationID: "one", ObservedAt: now},
	}
	for _, signal := range tests {
		if accepted, err := submit(records, signal, now); err == nil || accepted {
			t.Fatalf("unsafe signal accepted: %#v", signal)
		}
	}
}

func TestReputationRejectsMalformedAndStaleSignals(t *testing.T) {
	now := time.Now().UTC()
	testCases := []Signal{
		{IP: "bad-ip", Category: "injection", InstallationID: "one", ObservedAt: now},
		{IP: "0.0.0.0", Category: "injection", InstallationID: "one", ObservedAt: now},
		{IP: "224.0.0.1", Category: "injection", InstallationID: "one", ObservedAt: now},
		{IP: "203.0.113.10", Category: "injection", ObservedAt: now},
		{IP: "203.0.113.10", Category: "injection", InstallationID: strings.Repeat("x", 129), ObservedAt: now},
		{IP: "203.0.113.10", Category: "injection", InstallationID: "one"},
		{IP: "203.0.113.10", Category: "injection", InstallationID: "one", ObservedAt: now.Add(-evidenceWindow - time.Second)},
		{IP: "203.0.113.10", Category: "injection", InstallationID: "one", ObservedAt: now.Add(3 * time.Minute)},
	}
	for _, signal := range testCases {
		if accepted, err := submit(map[string]map[string]evidenceSource{}, signal, now); accepted || err == nil {
			t.Errorf("invalid signal accepted: %+v", signal)
		}
	}
	if cleaned := cleanEvidenceDescription("  scanner\n evidence\x00 "); cleaned != "scanner evidence" {
		t.Fatalf("cleaned description=%q", cleaned)
	}
	if cleaned := cleanEvidenceDescription(strings.Repeat("x", 300)); len(cleaned) != 240 {
		t.Fatalf("long description length=%d", len(cleaned))
	}
}

func TestReputationPersistsEvidence(t *testing.T) {
	now := time.Now().UTC()
	path := filepath.Join(t.TempDir(), "reputation.json")
	records := map[string]map[string]evidenceSource{}
	for _, installationID := range []string{"one", "two", "three"} {
		_, err := submit(records, Signal{
			IP:             "192.0.2.44",
			Category:       "repository",
			InstallationID: installationID,
			ObservedAt:     now,
		}, now)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := save(path, records, now); err != nil {
		t.Fatal(err)
	}
	loaded, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := approved(loaded, now)
	if len(entries) != 1 || entries[0].IP != "192.0.2.44" {
		t.Fatalf("persisted reputation missing: %#v", entries)
	}
	if err := save("", nil, now); err != nil {
		t.Fatalf("save without persistence path: %v", err)
	}
	filteredState := filepath.Join(t.TempDir(), "filtered.json")
	if err := os.WriteFile(filteredState, []byte(`{"records":[{"ip":"bad","sources":[{"installation_id":"one"}]},{"ip":"8.8.8.8","sources":[{"installation_id":""}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	filtered, err := load(filteredState)
	if err != nil || len(filtered) != 0 {
		t.Fatalf("filtered invalid state=%v err=%v", filtered, err)
	}
}

func TestApprovedOrderingLimitAndPruning(t *testing.T) {
	now := time.Now().UTC()
	records := map[string]map[string]evidenceSource{
		"203.0.113.1": {"one": {InstallationID: "one", Category: "injection", ObservedAt: now}},
		"203.0.113.2": {
			"one":   {InstallationID: "one", Category: "injection", ObservedAt: now},
			"two":   {InstallationID: "two", Category: "traversal", ObservedAt: now.Add(time.Second)},
			"three": {InstallationID: "three", Category: "secret", ObservedAt: now.Add(2 * time.Second)},
		},
	}
	for index := 0; index < 514; index++ {
		ip := fmt.Sprintf("198.18.%d.%d", index/256, index%256)
		records[ip] = map[string]evidenceSource{
			"one":   {InstallationID: "one", Category: "injection", ObservedAt: now},
			"two":   {InstallationID: "two", Category: "traversal", ObservedAt: now},
			"three": {InstallationID: "three", Category: "secret", ObservedAt: now},
		}
	}
	entries := approved(records, now)
	if len(entries) != 512 {
		t.Fatalf("approved entry count=%d", len(entries))
	}
	foundLatest := false
	for _, entry := range entries {
		if entry.IP == "203.0.113.2" && entry.Reason == "secret" {
			foundLatest = true
		}
	}
	if !foundLatest {
		t.Fatal("entry did not use its most recent source")
	}
	old := now.Add(-evidenceWindow - time.Second)
	records["203.0.113.99"] = map[string]evidenceSource{"old": {InstallationID: "old", ObservedAt: old}}
	prune(records, now)
	if _, exists := records["203.0.113.99"]; exists {
		t.Fatal("empty record remained after pruning")
	}
}

func TestPeerAttestationRoundTripAndExpiry(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	token, err := IssuePeerAttestation(privateKey, "installation-1", "client-public-key", now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	attestation, err := VerifyPeerAttestation(token, publicKey, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if attestation.InstallationID != "installation-1" || attestation.PublicKey != "client-public-key" {
		t.Fatalf("unexpected attestation: %#v", attestation)
	}
	if _, err := VerifyPeerAttestation(token, publicKey, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired attestation was accepted")
	}
}

func TestPeerAttestationRejectsInvalidInputsAndExpiryWindows(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := IssuePeerAttestation([]byte("bad"), "install", "key", now, time.Hour); err == nil {
		t.Fatal("invalid private key accepted")
	}
	if _, err := IssuePeerAttestation(privateKey, " ", "key", now, time.Hour); err == nil {
		t.Fatal("empty attestation identity accepted")
	}
	if _, err := VerifyPeerAttestation("!", publicKey, now); err == nil {
		t.Fatal("invalid token encoding accepted")
	}
	if _, err := VerifyPeerAttestation("e30", publicKey, now); err == nil {
		t.Fatal("invalid attestation payload accepted")
	}
	validToken, err := IssuePeerAttestation(privateKey, "install", "key", now, 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	attestationBytes, _ := base64.RawURLEncoding.DecodeString(validToken)
	var attestation PeerAttestation
	if err := json.Unmarshal(attestationBytes, &attestation); err != nil {
		t.Fatal(err)
	}
	attestation.ExpiresAt = now.Add(25 * time.Hour).Format(time.RFC3339)
	tooFarToken := signedTestAttestation(t, privateKey, attestation)
	if _, err := VerifyPeerAttestation(tooFarToken, publicKey, now); err == nil {
		t.Fatal("attestation expiring beyond the allowed window accepted")
	}
	attestation.ExpiresAt = now.Add(time.Hour).Format(time.RFC3339)
	attestation.InstallationID = " "
	missingIdentityToken := signedTestAttestation(t, privateKey, attestation)
	if _, err := VerifyPeerAttestation(missingIdentityToken, publicKey, now); err == nil {
		t.Fatal("attestation without identity accepted")
	}
	if _, err := VerifyPeerAttestation(validToken, publicKey[:4], now); err == nil {
		t.Fatal("invalid public key accepted")
	}
}

func signedTestAttestation(t *testing.T, privateKey ed25519.PrivateKey, attestation PeerAttestation) string {
	t.Helper()
	attestation.Signature = ""
	unsigned, err := json.Marshal(attestation)
	if err != nil {
		t.Fatal(err)
	}
	attestation.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, unsigned))
	encoded, err := json.Marshal(attestation)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}
