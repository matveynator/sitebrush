package diagnosticlog

import (
	"net"
	"net/netip"
	"strings"
	"time"
)

const tlsHandshakeErrorPrefix = "http: TLS handshake error from "

// TLSHandshakeNoise is a bounded grouping key and its first diagnostic sample.
type TLSHandshakeNoise struct {
	Class   string
	Source  string
	Message string
}

// ParseTLSHandshakeNoise admits only known client-side handshake noise so
// unknown TLS failures remain visible immediately.
func ParseTLSHandshakeNoise(message string) (TLSHandshakeNoise, bool) {
	cleanMessage := strings.TrimSpace(message)
	if !strings.HasPrefix(cleanMessage, tlsHandshakeErrorPrefix) {
		return TLSHandshakeNoise{}, false
	}
	remoteAndReason := strings.TrimPrefix(cleanMessage, tlsHandshakeErrorPrefix)
	remoteAddress, reason, found := strings.Cut(remoteAndReason, ": ")
	if !found {
		return TLSHandshakeNoise{}, false
	}
	remoteHost, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		return TLSHandshakeNoise{}, false
	}
	source, ok := tlsHandshakeSourceGroup(remoteHost)
	if !ok {
		return TLSHandshakeNoise{}, false
	}
	noiseClass, ok := tlsHandshakeNoiseClass(reason)
	if !ok {
		return TLSHandshakeNoise{}, false
	}
	return TLSHandshakeNoise{
		Class:   noiseClass,
		Source:  source,
		Message: cleanMessage,
	}, true
}

// TLSHandshakeNoiseInterval returns the delay after each emitted summary.
func TLSHandshakeNoiseInterval(summaryCount int) time.Duration {
	switch summaryCount {
	case 0:
		return 5 * time.Minute
	case 1:
		return 30 * time.Minute
	case 2:
		return time.Hour
	case 3:
		return 6 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func tlsHandshakeNoiseClass(reason string) (string, bool) {
	cleanReason := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case cleanReason == "eof",
		strings.Contains(cleanReason, "unexpected eof"),
		strings.Contains(cleanReason, "connection reset by peer"),
		strings.Contains(cleanReason, "broken pipe"),
		strings.Contains(cleanReason, "use of closed network connection"):
		return "connection_closed", true
	case strings.Contains(cleanReason, "i/o timeout"):
		return "timeout", true
	case strings.Contains(cleanReason, "remote error: tls: bad certificate"),
		strings.Contains(cleanReason, "remote error: tls: unknown certificate"),
		strings.Contains(cleanReason, "remote error: tls: certificate unknown"):
		return "certificate_rejected", true
	case strings.Contains(cleanReason, "first record does not look like a tls handshake"),
		strings.Contains(cleanReason, "client sent an http request to an https server"),
		strings.Contains(cleanReason, "client offered only unsupported versions"),
		strings.Contains(cleanReason, "no cipher suite supported by both client and server"):
		return "invalid_tls", true
	default:
		return "", false
	}
}

func tlsHandshakeSourceGroup(rawHost string) (string, bool) {
	address, err := netip.ParseAddr(strings.TrimSpace(rawHost))
	if err != nil {
		return "", false
	}
	address = address.Unmap().WithZone("")
	if address.IsLoopback() {
		return "loopback", true
	}
	prefixBits := 64
	if address.Is4() {
		prefixBits = 24
	}
	return netip.PrefixFrom(address, prefixBits).Masked().String(), true
}
