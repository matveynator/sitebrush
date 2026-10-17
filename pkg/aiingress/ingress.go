// Package aiingress admits provider networks independently of user credentials.
package aiingress

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"errors"
	"net/netip"
	"time"
)

// These snapshots come only from the providers' published outbound lists.
// A failed refresh must leave the last validated snapshot in use.
//
//go:embed openai-ranges.json
var openAIRanges string

//go:embed openai-root.pem
var openAIRoot string

//go:embed openai-intermediate.pem
var openAIIntermediate string

type Policy struct {
	XAICIDRs          []string `json:"xai_cidrs,omitempty"`
	External          bool     `json:"external"`
	OpenAI            bool     `json:"openai"`
	Anthropic         bool     `json:"anthropic"`
	XAI               bool     `json:"xai"`
	RequireOpenAIMTLS bool     `json:"require_openai_mtls"`
	CustomCIDRs       []string `json:"custom_cidrs,omitempty"`
}

type Ranges struct {
	OpenAI  []netip.Prefix
	Created time.Time
}

func ParseOpenAI(encoded []byte) (Ranges, error) {
	var document struct {
		CreationTime string `json:"creationTime"`
		Prefixes     []struct {
			IPv4 string `json:"ipv4Prefix"`
			IPv6 string `json:"ipv6Prefix"`
		} `json:"prefixes"`
	}
	if len(encoded) > 1<<20 || json.Unmarshal(encoded, &document) != nil || document.CreationTime == "" || len(document.Prefixes) == 0 || len(document.Prefixes) > 16384 {
		return Ranges{}, errors.New("invalid OpenAI range document")
	}
	created, err := time.Parse(time.RFC3339Nano, document.CreationTime)
	if err != nil {
		created, err = time.Parse("2006-01-02T15:04:05.999999999", document.CreationTime)
	}
	if err != nil || created.After(time.Now().Add(24*time.Hour)) {
		return Ranges{}, errors.New("invalid OpenAI range timestamp")
	}
	ranges := Ranges{Created: created}
	for _, entry := range document.Prefixes {
		prefixText := entry.IPv4
		if prefixText == "" {
			prefixText = entry.IPv6
		}
		prefix, err := netip.ParsePrefix(prefixText)
		if err != nil || prefix.Bits() == 0 {
			return Ranges{}, errors.New("invalid OpenAI prefix")
		}
		ranges.OpenAI = append(ranges.OpenAI, prefix.Masked())
	}
	return ranges, nil
}

func OfficialSnapshot() []byte { return []byte(openAIRanges) }

func (ranges Ranges) Allows(policy Policy, clientIP string, transport *tls.ConnectionState) bool {
	address, err := netip.ParseAddr(clientIP)
	if err != nil || !policy.External {
		return false
	}
	address = address.Unmap()
	if policy.OpenAI {
		for _, prefix := range ranges.OpenAI {
			if prefix.Contains(address) {
				// If a client presents a certificate, validate it even when optional.
				if policy.RequireOpenAIMTLS || transport != nil && len(transport.PeerCertificates) > 0 {
					return ValidOpenAIMTLS(transport, time.Now())
				}
				return true
			}
		}
	}
	if policy.Anthropic && netip.MustParsePrefix("160.79.104.0/21").Contains(address) {
		return true
	}
	// xAI has no verified stable outbound list. Explicit custom CIDRs are the
	// only admission mechanism until an official list can be verified.
	if policy.XAI {
		for _, configured := range policy.XAICIDRs {
			prefix, parseErr := netip.ParsePrefix(configured)
			if parseErr == nil && prefix.Bits() > 0 && prefix.Contains(address) {
				return true
			}
		}
	}
	for _, configured := range policy.CustomCIDRs {
		prefix, parseErr := netip.ParsePrefix(configured)
		if parseErr == nil && prefix.Bits() > 0 && prefix.Contains(address) {
			return true
		}
	}
	return false
}

func ValidOpenAIMTLS(transport *tls.ConnectionState, now time.Time) bool {
	if transport == nil || len(transport.PeerCertificates) == 0 {
		return false
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(openAIRoot)) {
		return false
	}
	intermediates := x509.NewCertPool()
	if !intermediates.AppendCertsFromPEM([]byte(openAIIntermediate)) {
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
