package outboundhttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("255.255.255.255/32"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type SourceOverride struct {
	Host    string
	Address net.IP
	Port    string
}

type TransportOptions struct {
	Resolver       Resolver
	Dialer         *net.Dialer
	SourceOverride SourceOverride
}

// NewTransport creates a transport that validates and pins every dialed address.
func NewTransport(baseTransport *http.Transport, options TransportOptions) (*http.Transport, error) {
	if baseTransport == nil {
		baseTransport = http.DefaultTransport.(*http.Transport)
	}
	transport := baseTransport.Clone()
	transport.Proxy = nil
	resolver := options.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := options.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 20 * time.Second}
	}
	sourceOverride := options.SourceOverride
	sourceOverride.Host = strings.ToLower(strings.TrimSpace(sourceOverride.Host))
	if sourceOverride.Address != nil && !IPAllowed(sourceOverride.Address) {
		return nil, errors.New("private network addresses are not allowed")
	}
	transport.DialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
		hostName, port, splitErr := net.SplitHostPort(address)
		if splitErr != nil || strings.TrimSpace(hostName) == "" || strings.TrimSpace(port) == "" {
			return nil, errors.New("source address is invalid")
		}
		if strings.Contains(hostName, "%") {
			return nil, errors.New("private network addresses are not allowed")
		}
		if sourceOverride.Address != nil && strings.EqualFold(hostName, sourceOverride.Host) {
			if sourceOverride.Port != "" {
				port = sourceOverride.Port
			}
			return dialPublicAddresses(ctx, dialer, network, port, []net.IPAddr{{IP: sourceOverride.Address}})
		}
		ipAddresses, lookupErr := resolver.LookupIPAddr(ctx, hostName)
		if lookupErr != nil {
			return nil, lookupErr
		}
		return dialPublicAddresses(ctx, dialer, network, port, ipAddresses)
	}
	return transport, nil
}

func dialPublicAddresses(ctx context.Context, dialer *net.Dialer, network string, port string, ipAddresses []net.IPAddr) (net.Conn, error) {
	if len(ipAddresses) == 0 {
		return nil, errors.New("source host did not resolve")
	}
	for _, ipAddress := range ipAddresses {
		if !IPAllowed(ipAddress.IP) {
			return nil, errors.New("private network addresses are not allowed")
		}
	}
	var lastErr error
	for _, ipAddress := range ipAddresses {
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ipAddress.IP.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("source host did not connect")
}

func CheckRedirect(request *http.Request, _ []*http.Request) error {
	if request == nil {
		return errors.New("source_url is invalid")
	}
	return RequirePublicURL(request.URL)
}

func RequirePublicURL(targetURL *url.URL) error {
	if targetURL == nil {
		return errors.New("source_url is invalid")
	}
	scheme := strings.ToLower(strings.TrimSpace(targetURL.Scheme))
	if scheme != "http" && scheme != "https" {
		return errors.New("source_url is invalid")
	}
	hostName := strings.TrimSpace(targetURL.Hostname())
	if hostName == "" || strings.Contains(hostName, "%") {
		return errors.New("source_url is invalid")
	}
	normalizedHostName := strings.ToLower(strings.TrimSuffix(hostName, "."))
	if normalizedHostName == "localhost" || strings.HasSuffix(normalizedHostName, ".localhost") {
		return errors.New("private network addresses are not allowed")
	}
	if targetURL.User != nil {
		return errors.New("source_url must not contain credentials")
	}
	if parsedIP := net.ParseIP(hostName); parsedIP != nil && !IPAllowed(parsedIP) {
		return errors.New("private network addresses are not allowed")
	}
	return nil
}

func IPAllowed(ip net.IP) bool {
	if ip == nil {
		return false
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() {
		return false
	}
	for _, blockedPrefix := range blockedPrefixes {
		if blockedPrefix.Contains(address) {
			return false
		}
	}
	return true
}
