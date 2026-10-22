package aiingress

import (
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"
)

func TestProviderNetworksRequireSitePolicyAndVerifiedRanges(t *testing.T) {
	ranges, err := ParseOpenAI(OfficialSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	openAIIP := ranges.OpenAI[0].Addr().String()
	for _, test := range []struct {
		name, ip string
		policy   Policy
		allowed  bool
	}{
		{"hidden", openAIIP, Policy{}, false},
		{"provider disabled", openAIIP, Policy{External: true}, false},
		{"OpenAI", openAIIP, Policy{External: true, OpenAI: true}, true},
		{"invalid OpenAI", "203.0.113.17", Policy{External: true, OpenAI: true}, false},
		{"Anthropic", "160.79.107.5", Policy{External: true, Anthropic: true}, true},
		{"Anthropic boundary", "160.79.112.1", Policy{External: true, Anthropic: true}, false},
		{"xAI no guessed ranges", "104.16.2.1", Policy{External: true}, false},
		{"explicit custom", "203.0.113.17", Policy{External: true, CustomCIDRs: []string{"203.0.113.0/24"}}, true},
		{"invalid custom", "203.0.113.17", Policy{External: true, CustomCIDRs: []string{"0.0.0.0/0", "bad"}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if ranges.Allows(test.policy, test.ip, nil) != test.allowed {
				t.Fatal("incorrect network admission")
			}
		})
	}
	forged := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{DNSNames: []string{"mtls.prod.connectors.openai.com"}}}}
	if ValidOpenAIMTLS(forged, time.Now()) || ranges.Allows(Policy{External: true, OpenAI: true}, openAIIP, forged) {
		t.Fatal("untrusted certificate admitted")
	}
	for _, document := range []string{`{}`, `{"creationTime":"now","prefixes":[{"ipv4Prefix":"0.0.0.0/0"}]}`, `{"creationTime":"now","prefixes":[{"ipv4Prefix":"broken"}]}`} {
		if _, err := ParseOpenAI([]byte(document)); err == nil {
			t.Fatal("invalid refresh accepted")
		}
	}
	if !ranges.Allows(Policy{External: true, OpenAI: true}, openAIIP, nil) {
		t.Fatal("failed refresh destroyed last known good ranges")
	}
}
