package accounttotp

import (
	"testing"
	"time"
)

func TestRFC6238SHA1Vector(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	code, err := Code(secret, time.Unix(59, 0))
	if err != nil {
		t.Fatal(err)
	}
	// RFC 6238 publishes eight digits; the six-digit HOTP truncation of the same value is 287082.
	if code != "287082" {
		t.Fatalf("unexpected code %q", code)
	}
	if !Verify(secret, code, time.Unix(59, 0)) {
		t.Fatal("generated code did not verify")
	}
}

func TestProvisioningURI(t *testing.T) {
	uri := ProvisioningURI("example.com", "owner@example.com", "ABCDEF")
	if uri == "" || uri[:10] != "otpauth://" {
		t.Fatalf("unexpected URI %q", uri)
	}
}
