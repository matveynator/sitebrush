package outboundhttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
)

type fixedResolver struct {
	addresses []net.IPAddr
}

func (resolver fixedResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return resolver.addresses, nil
}

type failingResolver struct{}

func (failingResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return nil, errors.New("lookup failed")
}

func TestTransportDialRejectsInvalidAndUnusableSources(t *testing.T) {
	transport, err := NewTransport(nil, TransportOptions{Resolver: fixedResolver{addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"bad-address", "[fe80::1%en0]:80", "example.com:80"} {
		_, err := transport.DialContext(context.Background(), "tcp", address)
		if err == nil {
			t.Errorf("DialContext(%q) succeeded", address)
		}
	}
	transport, err = NewTransport(nil, TransportOptions{Resolver: fixedResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.DialContext(context.Background(), "tcp", "example.com:80"); err == nil {
		t.Fatal("empty DNS answer accepted")
	}
	transport, err = NewTransport(nil, TransportOptions{Resolver: failingResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.DialContext(context.Background(), "tcp", "example.com:80"); err == nil || err.Error() != "lookup failed" {
		t.Fatalf("resolver error=%v", err)
	}
}

func TestTransportDialReturnsLastConnectionError(t *testing.T) {
	transport, err := NewTransport(nil, TransportOptions{Resolver: fixedResolver{addresses: []net.IPAddr{
		{IP: net.ParseIP("8.8.8.8")},
		{IP: net.ParseIP("1.1.1.1")},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.DialContext(context.Background(), "unsupported-network", "example.com:80"); err == nil {
		t.Fatal("connection errors were lost")
	}
}

func TestTransportDialUsesPublicSourceOverride(t *testing.T) {
	transport, err := NewTransport(nil, TransportOptions{SourceOverride: SourceOverride{
		Host: " EXAMPLE.COM ", Address: net.ParseIP("8.8.8.8"), Port: "443",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.DialContext(context.Background(), "unsupported-network", "example.com:80"); err == nil {
		t.Fatal("unsupported network unexpectedly connected")
	}
}

func TestIPAllowedRejectsInvalidAndAcceptsPublicAddress(t *testing.T) {
	if IPAllowed(nil) || IPAllowed(net.IP{1, 2, 3}) {
		t.Fatal("invalid IP address accepted")
	}
	if !IPAllowed(net.ParseIP("8.8.8.8")) {
		t.Fatal("public IP address rejected")
	}
}

func TestRequirePublicURLRejectsCredentialsAndPrivateAddresses(t *testing.T) {
	unsafeURLs := []string{
		"http://127.0.0.1/",
		"http://[::1]/",
		"http://[::ffff:127.0.0.1]/",
		"https://user:password@example.com/",
		"file:///etc/passwd",
	}
	for _, rawURL := range unsafeURLs {
		targetURL, _ := url.Parse(rawURL)
		if err := RequirePublicURL(targetURL); err == nil {
			t.Fatalf("RequirePublicURL(%q) allowed an unsafe target", rawURL)
		}
	}
}

func TestRequirePublicURLRejectsMalformedAndLocalNames(t *testing.T) {
	for _, rawURL := range []string{"http://", "http://localhost/", "https://api.localhost/", "http://[fe80::1%25en0]/"} {
		targetURL, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		if err := RequirePublicURL(targetURL); err == nil {
			t.Errorf("RequirePublicURL(%q) unexpectedly succeeded", rawURL)
		}
	}
	if err := RequirePublicURL(nil); err == nil {
		t.Fatal("nil URL accepted")
	}
}

func TestCheckRedirectValidatesRedirectTarget(t *testing.T) {
	if err := CheckRedirect(nil, nil); err == nil {
		t.Fatal("nil request accepted")
	}
	for _, rawURL := range []string{"http://127.0.0.1/path", "file:///tmp/sitebrush"} {
		request, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckRedirect(request, nil); err == nil {
			t.Fatalf("redirect to %q accepted", rawURL)
		}
	}
	request, err := http.NewRequest(http.MethodGet, "https://example.com/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckRedirect(request, nil); err != nil {
		t.Fatalf("public redirect rejected: %v", err)
	}
}

func TestNewTransportRejectsMixedPublicAndPrivateDNSResults(t *testing.T) {
	transport, err := NewTransport(http.DefaultTransport.(*http.Transport), TransportOptions{
		Resolver: fixedResolver{addresses: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("127.0.0.1")}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.DialContext(context.Background(), "tcp", "example.com:80")
	if err == nil {
		t.Fatal("mixed public and private DNS result was allowed")
	}
}

func TestNewTransportRejectsPrivateSourceOverride(t *testing.T) {
	_, err := NewTransport(http.DefaultTransport.(*http.Transport), TransportOptions{
		SourceOverride: SourceOverride{Host: "example.com", Address: net.ParseIP("169.254.169.254")},
	})
	if err == nil {
		t.Fatal("private source override was allowed")
	}
}
