// Package aiingress admits provider networks independently of user credentials.
package aiingress

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/netip"
	"time"
)

type Policy struct {
	External    bool     `json:"external"`
	OpenAI      bool     `json:"openai"`
	Anthropic   bool     `json:"anthropic"`
	Perplexity  bool     `json:"perplexity"`
	Google      bool     `json:"google"`
	Microsoft   bool     `json:"microsoft"`
	Apple       bool     `json:"apple"`
	Amazon      bool     `json:"amazon"`
	XAI         bool     `json:"xai"`
	CustomCIDRs []string `json:"custom_cidrs,omitempty"`
}

func DefaultPolicy() Policy {
	return Policy{External: true, OpenAI: true, Anthropic: true, Perplexity: true, Google: true, Microsoft: true, Apple: true, Amazon: true, XAI: true}
}

// Missing settings inherit defaults; explicitly saved exclusions remain effective.
func DecodePolicy(encoded []byte) (Policy, error) {
	policy := DefaultPolicy()
	encoded = bytes.TrimSpace(encoded)
	if len(encoded) == 0 || encoded[0] != '{' {
		return Policy{}, errors.New("invalid AI ingress policy")
	}
	if err := json.Unmarshal(encoded, &policy); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func (policy Policy) Enabled(provider string) bool {
	switch provider {
	case "openai":
		return policy.OpenAI
	case "anthropic":
		return policy.Anthropic
	case "perplexity":
		return policy.Perplexity
	case "google":
		return policy.Google
	case "microsoft":
		return policy.Microsoft
	case "apple":
		return policy.Apple
	case "amazon":
		return policy.Amazon
	case "xai":
		return policy.XAI
	}
	return false
}

type Ranges struct {
	OpenAI  []netip.Prefix
	Created time.Time
}

func ParseOpenAI(encoded []byte) (Ranges, error) {
	snapshot, err := ParseSnapshot(encoded)
	return Ranges{OpenAI: snapshot.Prefixes, Created: snapshot.Created}, err
}

func OfficialSnapshot() []byte { return OfficialSourceSnapshot("openai") }

func (ranges Ranges) Allows(policy Policy, clientIP string, transport *tls.ConnectionState) bool {
	address, err := netip.ParseAddr(clientIP)
	if err != nil || !policy.External {
		return false
	}
	address = address.Unmap()
	if policy.OpenAI && contains(ranges.OpenAI, address) {
		return openAICertificateAllowed(transport)
	}
	if policy.XAI && (netip.MustParsePrefix("69.12.56.0/21").Contains(address) || netip.MustParsePrefix("31.207.0.0/24").Contains(address)) {
		return true
	}
	if policy.Anthropic && netip.MustParsePrefix("160.79.104.0/21").Contains(address) {
		return true
	}
	for _, configured := range policy.CustomCIDRs {
		prefix, parseErr := netip.ParsePrefix(configured)
		if parseErr == nil && prefix.Bits() > 0 && prefix.Contains(address) {
			return true
		}
	}
	return false
}

func contains(prefixes []netip.Prefix, address netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func openAICertificateAllowed(transport *tls.ConnectionState) bool {
	// Certificates are optional; a presented identity must still be authentic.
	return transport == nil || len(transport.PeerCertificates) == 0 || ValidOpenAIMTLS(transport, time.Now())
}

func ValidOpenAIMTLS(transport *tls.ConnectionState, now time.Time) bool {
	if transport == nil || len(transport.PeerCertificates) == 0 {
		return false
	}
	roots := x509.NewCertPool()
	rootPEM, _ := officialFiles.ReadFile("openai-root.pem")
	if !roots.AppendCertsFromPEM(rootPEM) {
		return false
	}
	intermediates := x509.NewCertPool()
	intermediatePEM, _ := officialFiles.ReadFile("openai-intermediate.pem")
	if !intermediates.AppendCertsFromPEM(intermediatePEM) {
		return false
	}
	leaf := transport.PeerCertificates[0]
	nameFound := false
	for _, name := range leaf.DNSNames {
		if name == "mtls.prod.connectors.openai.com" {
			nameFound = true
		}
	}
	if !nameFound {
		return false
	}
	_, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
	return err == nil
}
