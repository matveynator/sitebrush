package securitysync

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestSecurityBoundaryPeerAttestationRejectsInstallationIdentitySubstitution(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_200_000, 0).UTC()
	token, err := IssuePeerAttestation(privateKey, "installation-a", "peer-key-a", now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	var attestation PeerAttestation
	if err := json.Unmarshal(encoded, &attestation); err != nil {
		t.Fatal(err)
	}
	attestation.InstallationID = "installation-b"
	tampered, err := json.Marshal(attestation)
	if err != nil {
		t.Fatal(err)
	}
	tamperedToken := base64.RawURLEncoding.EncodeToString(tampered)
	if _, err := VerifyPeerAttestation(tamperedToken, publicKey, now); err == nil {
		t.Fatal("SECURITY: signed peer attestation accepted a substituted installation ID")
	}
}

func TestSecurityBoundaryPeerAttestationRejectsPublicKeySubstitution(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_200_100, 0).UTC()
	token, err := IssuePeerAttestation(privateKey, "installation-a", "peer-key-a", now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	var attestation PeerAttestation
	if err := json.Unmarshal(encoded, &attestation); err != nil {
		t.Fatal(err)
	}
	attestation.PublicKey = "attacker-peer-key"
	tampered, err := json.Marshal(attestation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPeerAttestation(base64.RawURLEncoding.EncodeToString(tampered), publicKey, now); err == nil {
		t.Fatal("SECURITY: signed peer attestation accepted a substituted public key")
	}
}

func TestSecurityBoundaryPeerAttestationRejectsAttackerSigner(t *testing.T) {
	centralPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, attackerPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_200_200, 0).UTC()
	forged, err := IssuePeerAttestation(attackerPrivateKey, "installation-forged", "peer-key-forged", now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPeerAttestation(forged, centralPublicKey, now); err == nil {
		t.Fatal("SECURITY: peer attestation signed by an attacker-controlled key was accepted")
	}
}

func TestSecurityBoundaryPeerAttestationReplayExpires(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_200_300, 0).UTC()
	token, err := IssuePeerAttestation(privateKey, "installation-a", "peer-key-a", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPeerAttestation(token, publicKey, now.Add(9*time.Minute)); err != nil {
		t.Fatalf("valid attestation rejected before expiry: %v", err)
	}
	if _, err := VerifyPeerAttestation(token, publicKey, now.Add(10*time.Minute)); err == nil {
		t.Fatal("SECURITY: expired peer attestation was replayed successfully")
	}
}

func TestSecurityBoundaryOneInstallationCannotForgeReputationQuorumByReplay(t *testing.T) {
	now := time.Unix(1_800_200_400, 0).UTC()
	records := map[string]map[string]evidenceSource{}
	for attempt := 0; attempt < requiredSources+5; attempt++ {
		accepted, err := submit(records, Signal{
			IP:             "203.0.113.88",
			Category:       "injection",
			Description:    "replayed evidence",
			InstallationID: "single-installation",
			ObservedAt:     now.Add(time.Duration(attempt) * time.Second),
		}, now.Add(time.Duration(attempt)*time.Second))
		if err != nil || !accepted {
			t.Fatalf("replay %d: accepted=%v err=%v", attempt, accepted, err)
		}
	}
	if got := approved(records, now.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("SECURITY: one installation forged quorum by replaying signals: %#v", got)
	}
}

func TestSecurityBoundaryOldSignalReplayCannotExtendConfirmedBlock(t *testing.T) {
	originalObservedAt := time.Unix(1_800_200_500, 0).UTC()
	records := map[string]map[string]evidenceSource{}
	installationIDs := []string{"installation-a", "installation-b", "installation-c"}
	for sourceIndex := 0; sourceIndex < requiredSources; sourceIndex++ {
		accepted, err := submit(records, Signal{
			IP:             "203.0.113.89",
			Category:       "injection",
			Description:    "original incident",
			InstallationID: installationIDs[sourceIndex],
			ObservedAt:     originalObservedAt,
		}, originalObservedAt)
		if err != nil || !accepted {
			t.Fatalf("initial signal %d: accepted=%v err=%v", sourceIndex, accepted, err)
		}
	}

	initialEntries := approved(records, originalObservedAt.Add(time.Minute))
	if len(initialEntries) != 1 || initialEntries[0].Confirmations != requiredSources {
		t.Fatalf("initial reputation entries = %#v", initialEntries)
	}
	initialExpiry := initialEntries[0].ExpiresAt

	for replayIndex := 0; replayIndex < 10_000; replayIndex++ {
		replayNow := originalObservedAt.Add(time.Minute + time.Duration(replayIndex)*time.Second)
		accepted, err := submit(records, Signal{
			IP:             "203.0.113.89",
			Category:       "injection",
			Description:    "replayed incident",
			InstallationID: "installation-a",
			ObservedAt:     originalObservedAt,
		}, replayNow)
		if err != nil || !accepted {
			t.Fatalf("replay %d: accepted=%v err=%v", replayIndex, accepted, err)
		}
	}

	finalEntries := approved(records, originalObservedAt.Add(2*time.Hour))
	if len(finalEntries) != 1 {
		t.Fatalf("replayed reputation entries = %#v", finalEntries)
	}
	if finalEntries[0].Confirmations != requiredSources || !finalEntries[0].ExpiresAt.Equal(initialExpiry) {
		t.Fatalf("SECURITY: old signal replay changed confirmation/expiry: initial=%s final=%#v", initialExpiry, finalEntries[0])
	}
}
