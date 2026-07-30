package outboundhttp

import (
	"context"
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
