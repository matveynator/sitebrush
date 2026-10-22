package aiingress

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Published documents are immutable bootstrap data, available before networking.
//
//go:embed snapshots/*.json openai-root.pem openai-intermediate.pem
var officialFiles embed.FS

type Provider struct {
	ID   string
	Name string
}

func Providers() []Provider {
	return []Provider{
		{"openai", "OpenAI / ChatGPT / Codex"},
		{"anthropic", "Anthropic / Claude"},
		{"perplexity", "Perplexity"},
		{"google", "Google / Gemini / NotebookLM"},
		{"microsoft", "Microsoft / Bing"},
		{"apple", "Apple / Siri / Apple Intelligence"},
		{"amazon", "Amazon / Alexa"},
		{"xai", "xAI / Grok / X / SpaceX / Starlink"},
	}
}

type Source struct {
	ID       string
	Provider string
	URL      string
	HTML     bool
	ASN      int
}

func Sources() []Source {
	return []Source{
		{"openai", "openai", "https://openai.com/chatgpt-connectors.json", false, 0},
		{"openai-agents", "openai", "https://openai.com/chatgpt-agents.json", false, 0},
		{"openai-user", "openai", "https://openai.com/chatgpt-user.json", false, 0},
		{"openai-search", "openai", "https://openai.com/searchbot.json", false, 0},
		{"openai-gptbot", "openai", "https://openai.com/gptbot.json", false, 0},
		{"openai-adsbot", "openai", "https://openai.com/adsbot.json", false, 0},
		{"perplexity-bot", "perplexity", "https://www.perplexity.com/perplexitybot.json", false, 0},
		{"perplexity-user", "perplexity", "https://www.perplexity.com/perplexity-user.json", false, 0},
		{"google-agents", "google", "https://developers.google.com/static/crawling/ipranges/user-triggered-agents.json", false, 0},
		{"google-fetchers", "google", "https://developers.google.com/static/crawling/ipranges/user-triggered-fetchers-google.json", false, 0},
		{"microsoft", "microsoft", "https://www.bing.com/toolbox/bingbot.json", false, 0},
		{"apple", "apple", "https://search.developer.apple.com/applebot.json", false, 0},
		{"amazon-bot", "amazon", "https://developer.amazon.com/amazonbot/ip-addresses/", true, 0},
		{"amazon-search", "amazon", "https://developer.amazon.com/amazonbot/searchbot-ip-addresses/", true, 0},
		{"amazon-user", "amazon", "https://developer.amazon.com/amazonbot/live-ip-addresses/", true, 0},
		{"xai-as63179", "xai", "https://stat.ripe.net/data/announced-prefixes/data.json?resource=AS63179", false, 63179},
		{"xai-as400297", "xai", "https://stat.ripe.net/data/announced-prefixes/data.json?resource=AS400297", false, 400297},
		{"xai-as13414", "xai", "https://stat.ripe.net/data/announced-prefixes/data.json?resource=AS13414", false, 13414},
		{"xai-as35995", "xai", "https://stat.ripe.net/data/announced-prefixes/data.json?resource=AS35995", false, 35995},
		{"xai-as54888", "xai", "https://stat.ripe.net/data/announced-prefixes/data.json?resource=AS54888", false, 54888},
		{"xai-as27277", "xai", "https://stat.ripe.net/data/announced-prefixes/data.json?resource=AS27277", false, 27277},
		{"xai-as14593", "xai", "https://stat.ripe.net/data/announced-prefixes/data.json?resource=AS14593", false, 14593},
		{"xai-as53348", "xai", "https://stat.ripe.net/data/announced-prefixes/data.json?resource=AS53348", false, 53348},
		{"xai-as397763", "xai", "https://stat.ripe.net/data/announced-prefixes/data.json?resource=AS397763", false, 397763},
	}
}

func OfficialSourceSnapshot(source string) []byte {
	for _, registered := range Sources() {
		if source == registered.ID {
			encoded, _ := officialFiles.ReadFile("snapshots/" + source + ".json")
			return encoded
		}
	}
	return nil
}

type Snapshot struct {
	Prefixes []netip.Prefix
	Created  time.Time
}

func ParseSnapshot(encoded []byte) (Snapshot, error) {
	return parseSnapshot(encoded, false)
}

func (source Source) Snapshot(encoded []byte) (Snapshot, error) {
	return parseSnapshot(encoded, source.ASN != 0)
}

func parseSnapshot(encoded []byte, allowEmpty bool) (Snapshot, error) {
	var document struct {
		CreationTime string `json:"creationTime"`
		Prefixes     []struct {
			IPv4 string `json:"ipv4Prefix"`
			IPv6 string `json:"ipv6Prefix"`
			IP   string `json:"ip_prefix"`
		} `json:"prefixes"`
	}
	if len(encoded) > 1<<20 || json.Unmarshal(encoded, &document) != nil || document.Prefixes == nil || !allowEmpty && len(document.Prefixes) == 0 || len(document.Prefixes) > 16384 {
		return Snapshot{}, errors.New("invalid official AI range document")
	}
	created, err := time.Parse(time.RFC3339Nano, document.CreationTime)
	if err != nil {
		created, err = time.Parse("2006-01-02T15:04:05.999999999", document.CreationTime)
	}
	if err != nil || created.After(time.Now().Add(24*time.Hour)) {
		return Snapshot{}, errors.New("invalid official AI range timestamp")
	}
	snapshot := Snapshot{Created: created}
	for _, entry := range document.Prefixes {
		prefixText := entry.IPv4
		if prefixText == "" {
			prefixText = entry.IPv6
		}
		if prefixText == "" {
			prefixText = entry.IP
		}
		prefix, parseErr := netip.ParsePrefix(prefixText)
		if parseErr != nil {
			// Amazon also publishes individual hosts without a CIDR suffix.
			address, addressErr := netip.ParseAddr(prefixText)
			if addressErr != nil {
				return Snapshot{}, errors.New("invalid official AI prefix")
			}
			prefix = netip.PrefixFrom(address, address.BitLen())
		}
		if prefix.Bits() == 0 || !prefix.Addr().IsGlobalUnicast() || prefix.Addr().IsPrivate() || prefix.Addr().IsLoopback() || prefix.Addr().Is4In6() {
			return Snapshot{}, errors.New("invalid official AI prefix")
		}
		snapshot.Prefixes = append(snapshot.Prefixes, prefix.Masked())
	}
	return snapshot, nil
}

// Amazon's official pages wrap their range document in a code block. Extract
// that document only; unrelated addresses in page navigation never grant access.
func (source Source) Document(encoded []byte) ([]byte, error) {
	if len(encoded) > 1<<20 {
		return nil, errors.New("official AI document exceeds limit")
	}
	if source.ASN != 0 {
		return source.routingDocument(encoded)
	}
	if !source.HTML {
		_, err := ParseSnapshot(encoded)
		return encoded, err
	}
	tokens := html.NewTokenizer(bytes.NewReader(encoded))
	var block strings.Builder
	inside := false
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			if tokens.Err() != io.EOF {
				return nil, tokens.Err()
			}
			return nil, errors.New("official AI range document missing")
		case html.StartTagToken:
			name, _ := tokens.TagName()
			if string(name) == "code" {
				inside = true
				block.Reset()
			}
		case html.TextToken:
			if inside {
				block.Write(tokens.Text())
			}
		case html.EndTagToken:
			name, _ := tokens.TagName()
			if string(name) == "code" && inside {
				inside = false
				document := []byte(block.String())
				if _, err := ParseSnapshot(document); err == nil {
					return document, nil
				}
			}
		}
	}
}

// ASN admission is an explicit policy choice for the whole xAI/X/SpaceX group,
// including subscriber networks. Only currently visible origin prefixes count;
// a route withdrawn earlier in the query window must not retain admission.
func (source Source) routingDocument(encoded []byte) ([]byte, error) {
	var response struct {
		Status string `json:"status"`
		Data   struct {
			Resource string `json:"resource"`
			End      string `json:"query_endtime"`
			Prefixes []struct {
				Prefix    string `json:"prefix"`
				Timelines []struct {
					End string `json:"endtime"`
				} `json:"timelines"`
			} `json:"prefixes"`
		} `json:"data"`
	}
	if json.Unmarshal(encoded, &response) != nil || response.Status != "ok" || strings.TrimPrefix(response.Data.Resource, "AS") != strconv.Itoa(source.ASN) || response.Data.End == "" || response.Data.Prefixes == nil {
		return nil, errors.New("invalid official ASN range document")
	}
	var document struct {
		Created  string              `json:"creationTime"`
		Prefixes []map[string]string `json:"prefixes"`
	}
	document.Created = response.Data.End + "Z"
	document.Prefixes = []map[string]string{}
	for _, announced := range response.Data.Prefixes {
		for _, timeline := range announced.Timelines {
			if timeline.End >= response.Data.End {
				document.Prefixes = append(document.Prefixes, map[string]string{"ip_prefix": announced.Prefix})
				break
			}
		}
	}
	normalized, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	_, err = source.Snapshot(normalized)
	return normalized, err
}
