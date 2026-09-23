package accountauth

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Proxy headers confer trust only through an explicitly trusted transport peer.
// Missing forwarding information from a proxy is not a shared client identity.
func ClientIP(request *http.Request, trustedCIDRs string) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	peer := net.ParseIP(host)
	if peer == nil {
		return ""
	}
	trusted := func(ip net.IP) bool {
		if strings.TrimSpace(trustedCIDRs) == "" {
			return ip.IsLoopback()
		}
		for _, raw := range strings.Split(trustedCIDRs, ",") {
			text := strings.TrimSpace(raw)
			if address := net.ParseIP(text); address != nil && address.Equal(ip) {
				return true
			}
			_, network, parseErr := net.ParseCIDR(text)
			if parseErr == nil && network.Contains(ip) {
				return true
			}
		}
		return false
	}
	if !trusted(peer) {
		return peer.String()
	}
	chain := []string{}
	if forwarded := request.Header.Get("Forwarded"); forwarded != "" {
		for _, entry := range strings.Split(forwarded, ",") {
			address := ""
			for _, part := range strings.Split(entry, ";") {
				pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
				if len(pair) == 2 && strings.EqualFold(pair[0], "for") {
					address = strings.Trim(pair[1], "\"")
				}
			}
			if hostname, _, splitErr := net.SplitHostPort(address); splitErr == nil {
				address = hostname
			}
			chain = append(chain, strings.Trim(address, "[]"))
		}
	} else {
		chain = strings.Split(request.Header.Get("X-Forwarded-For"), ",")
	}
	for index := len(chain) - 1; index >= 0; index-- {
		candidate := net.ParseIP(strings.TrimSpace(chain[index]))
		if candidate == nil {
			return ""
		}
		if !trusted(candidate) {
			return candidate.String()
		}
	}
	return ""
}

func SafeQuery(raw string) string {
	if raw == "" {
		return ""
	}
	query, err := url.ParseQuery(raw)
	if err != nil {
		return "[redacted]"
	}
	for key := range query {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "code") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "challenge") || strings.Contains(lower, "resume") || lower == "email_confirm" || lower == "return_path" {
			query.Set(key, "[redacted]")
		}
	}
	return query.Encode()
}
