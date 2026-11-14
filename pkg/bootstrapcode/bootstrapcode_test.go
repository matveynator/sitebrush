package bootstrapcode

import "testing"

func TestInstallCodeAllowsValidSetupAfterFailedAttempts(t *testing.T) {
	storagePath := t.TempDir()
	code, err := Issue(storagePath, false)
	if err != nil || len(code) != 48 {
		t.Fatalf("issue code length=%d err=%v", len(code), err)
	}
	stop := make(chan struct{})
	defer close(stop)
	requests := Start(storagePath, stop)
	for range 7 {
		if Verify(requests, "000000000000000000000000000000000000000000000000", false, stop) {
			t.Fatal("incorrect install code accepted")
		}
	}
	if !Verify(requests, code, false, stop) || !Verify(requests, code, false, stop) {
		t.Fatal("correct install code rejected before account creation")
	}
	if !Verify(requests, code, true, stop) || Verify(requests, code, false, stop) {
		t.Fatal("consumed install code remained usable")
	}
}
