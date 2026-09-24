package outboundhttp

import (
	"context"
	"net"
	"net/url"
	"strings"
	"testing"
)

type rebindingSecurityResolver struct {
	calls int
}

func (resolver *rebindingSecurityResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	resolver.calls++
	if resolver.calls == 1 {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
}

func TestSecurityBoundaryTransportRevalidatesDNSOnEveryDial(t *testing.T) {
	resolver := &rebindingSecurityResolver{}
	transport, err := NewTransport(nil, TransportOptions{Resolver: resolver})
	if err != nil {
		t.Fatal(err)
	}

	// The first lookup is public. An unsupported network avoids making any
	// external connection while still exercising address validation.
	if _, err := transport.DialContext(context.Background(), "unsupported-network", "example.com:80"); err == nil {
		t.Fatal("first public resolution unexpectedly connected")
	}

	// A rebinding answer must be validated again rather than reusing the first
	// public result or opening a socket to the new loopback address.
	if _, err := transport.DialContext(context.Background(), "tcp", "example.com:80"); err == nil || !strings.Contains(err.Error(), "private network") {
		t.Fatalf("SECURITY: DNS rebinding to loopback was not rejected: %v", err)
	}
	if resolver.calls != 2 {
		t.Fatalf("resolver calls = %d, want 2 independent validations", resolver.calls)
	}
}

func TestSecurityBoundaryRequirePublicURLRejectsMappedAndMetadataAddresses(t *testing.T) {
	for _, rawURL := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://[::ffff:169.254.169.254]/",
		"http://[fe80::1]/",
		"http://localhost./",
		"http://metadata.localhost/",
	} {
		target, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		if err := RequirePublicURL(target); err == nil {
			t.Fatalf("SECURITY: private or metadata target was accepted: %s", rawURL)
		}
	}
}
