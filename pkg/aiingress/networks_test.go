package aiingress

import (
	"fmt"
	"testing"
	"time"
)

func TestDefaultProviderNetworksAndLegacyPolicy(t *testing.T) {
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	networks := NewNetworks(stop, nil)
	policy, err := DecodePolicy([]byte(`{"require_openai_mtls":true,"xai_cidrs":["203.0.113.0/24"]}`))
	if err != nil || !policy.External {
		t.Fatal(policy, err)
	}
	for _, provider := range Providers() {
		if !policy.Enabled(provider.ID) {
			t.Fatalf("provider disabled by default: %s", provider.ID)
		}
	}
	for _, source := range Sources() {
		t.Run(source.ID, func(t *testing.T) {
			snapshot, err := source.Snapshot(OfficialSourceSnapshot(source.ID))
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Prefixes) == 0 {
				return
			}
			address := snapshot.Prefixes[0].Addr().String()
			if !networks.Allows(policy, address, nil, nil) {
				t.Fatalf("official address rejected: %s", address)
			}
		})
	}
	if !networks.Allows(policy, "160.79.104.1", nil, nil) {
		t.Fatal("Claude outbound MCP range rejected")
	}
	for _, address := range []string{"69.12.56.1", "31.207.0.1"} {
		if !networks.Allows(policy, address, nil, nil) {
			t.Fatalf("requested xAI network rejected: %s", address)
		}
	}
	for _, address := range []string{"203.0.113.8", "127.0.0.1", "::1", "10.1.2.3", "bad"} {
		if networks.Allows(policy, address, nil, nil) {
			t.Fatalf("unknown address admitted: %s", address)
		}
	}
	policy, err = DecodePolicy([]byte(`{"external":false,"openai":false}`))
	if err != nil || policy.External || policy.OpenAI {
		t.Fatal("explicit opt-out lost", policy, err)
	}
	if networks.Allows(policy, "160.79.104.1", nil, nil) {
		t.Fatal("disabled policy admitted provider")
	}
	for _, encoded := range []string{"null", " null ", "[]", "broken"} {
		if _, err := DecodePolicy([]byte(encoded)); err == nil {
			t.Fatalf("invalid policy accepted: %s", encoded)
		}
	}
}

func TestASNRefreshExcludesWithdrawnRoutesAndChecksIdentity(t *testing.T) {
	source := Source{ASN: 63179}
	encoded := []byte(`{"status":"ok","data":{"resource":"63179","query_endtime":"2026-10-21T00:00:00","prefixes":[{"prefix":"198.51.100.0/24","timelines":[{"endtime":"2026-10-21T00:00:00"}]},{"prefix":"203.0.113.0/24","timelines":[{"endtime":"2026-10-20T00:00:00"}]}]}}`)
	document, err := source.Document(encoded)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.Snapshot(document)
	if err != nil || len(snapshot.Prefixes) != 1 || snapshot.Prefixes[0].String() != "198.51.100.0/24" {
		t.Fatal("withdrawn routes retained", snapshot, err)
	}
	if _, err := (Source{ASN: 400297}).Document(encoded); err == nil {
		t.Fatal("unrelated ASN accepted")
	}
	for _, prefixes := range []string{`[]`, `null`} {
		empty := []byte(fmt.Sprintf(`{"status":"ok","data":{"resource":"63179","query_endtime":"2026-10-21T00:00:00","prefixes":%s}}`, prefixes))
		document, err := source.Document(empty)
		if prefixes == "null" {
			if err == nil {
				t.Fatal("incomplete routing document accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := source.Snapshot(document)
		if err != nil || len(snapshot.Prefixes) != 0 {
			t.Fatal("unannounced ASN supplied addresses", snapshot, err)
		}
	}
}

func TestOfficialSnapshotFormatsAndFailedRefresh(t *testing.T) {
	created := time.Now().UTC().Format(time.RFC3339Nano)
	document := []byte(fmt.Sprintf(`{"creationTime":%q,"prefixes":[{"ip_prefix":"198.51.100.7"},{"ipv6Prefix":"2001:db8::/48"}]}`, created))
	source := Source{HTML: true}
	wrapped := append([]byte(`<html><p>0.0.0.0/0</p><code>invalid</code><pre><code>`), document...)
	wrapped = append(wrapped, []byte(`</code></pre></html>`)...)
	if parsed, err := source.Document(wrapped); err != nil || string(parsed) != string(document) {
		t.Fatal("official code block lost", err)
	}
	for _, encoded := range []string{
		`<p>198.51.100.7</p>`,
		fmt.Sprintf(`{"creationTime":%q,"prefixes":[{"ipv4Prefix":"0.0.0.0/0"}]}`, created),
		fmt.Sprintf(`{"creationTime":%q,"prefixes":[{"ipv4Prefix":"127.0.0.1/32"}]}`, created),
		fmt.Sprintf(`{"creationTime":%q,"prefixes":[{"ipv4Prefix":"bad"}]}`, created),
	} {
		if _, err := (Source{}).Document([]byte(encoded)); err == nil {
			t.Fatalf("invalid refresh accepted: %s", encoded)
		}
	}
	stop := make(chan struct{})
	networks := NewNetworks(stop, nil)
	defer close(stop)
	policy := Policy{External: true, Perplexity: true}
	if err := networks.Replace("perplexity-user", document, nil); err != nil {
		t.Fatal(err)
	}
	if !networks.Allows(policy, "198.51.100.7", nil, nil) || !networks.Allows(policy, "2001:db8::1", nil, nil) {
		t.Fatal("valid update not installed")
	}
	for _, replacement := range [][]byte{[]byte(`broken`), []byte(`{"creationTime":"2020-01-01T00:00:00Z","prefixes":[{"ipv4Prefix":"203.0.113.0/24"}]}`)} {
		if err := networks.Replace("perplexity-user", replacement, nil); err == nil {
			t.Fatal("invalid or older update accepted")
		}
		if !networks.Allows(policy, "198.51.100.7", nil, nil) {
			t.Fatal("failed refresh destroyed working ranges")
		}
	}
	if err := networks.Replace("unregistered", document, nil); err == nil {
		t.Fatal("unknown source installed")
	}
	cancelled := make(chan struct{})
	close(cancelled)
	if networks.Allows(policy, "198.51.100.7", nil, cancelled) {
		t.Fatal("cancelled admission continued")
	}
	if err := networks.Replace("perplexity-user", document, cancelled); err == nil {
		t.Fatal("cancelled refresh continued")
	}
}
