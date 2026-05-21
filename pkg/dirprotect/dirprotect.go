package dirprotect

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

type Rule struct {
	Domain       string
	Path         string
	PasswordHash string
}

func CleanPath(rawPath string) string {
	trimmedPath := strings.TrimSpace(rawPath)
	if trimmedPath == "" {
		return "/"
	}
	if !strings.HasPrefix(trimmedPath, "/") {
		trimmedPath = "/" + trimmedPath
	}
	normalizedPath := path.Clean(trimmedPath)
	if normalizedPath == "." || normalizedPath == "" {
		return "/"
	}
	return normalizedPath
}

func HasProtectedPrefix(pagePath, protectedPrefix string) bool {
	pagePath = CleanPath(pagePath)
	protectedPrefix = CleanPath(protectedPrefix)
	if protectedPrefix == "/" {
		return true
	}
	return pagePath == protectedPrefix || strings.HasPrefix(pagePath, protectedPrefix+"/")
}

func Hash(password string) string {
	hashedBytes := sha256.Sum256([]byte("sitebrush page password\n" + password))
	return fmt.Sprintf("sha256:%x", hashedBytes)
}

func Matches(storedHash, password string) bool {
	return strings.TrimSpace(storedHash) == Hash(password)
}

func CookieName(domain, pagePath string) string {
	hashedBytes := sha256.Sum256([]byte(NormalizeDomain(domain) + "\n" + CleanPath(pagePath)))
	return "sitebrush_page_password_" + fmt.Sprintf("%x", hashedBytes)[:16]
}

func BoundSessionToken(rule Rule, clientIP, userAgent string, issuedAt time.Time) string {
	issuedUnix := issuedAt.UTC().Unix()
	return "v2:" + strconv.FormatInt(issuedUnix, 10) + ":" + boundSessionSignature(rule, clientIP, issuedUnix)
}

func BoundSessionTokenValid(rule Rule, token, clientIP, userAgent string, now time.Time, ttl time.Duration) bool {
	if ttl <= 0 {
		return false
	}
	tokenParts := strings.Split(strings.TrimSpace(token), ":")
	if len(tokenParts) != 3 || tokenParts[0] != "v2" {
		return false
	}
	issuedUnix, err := strconv.ParseInt(tokenParts[1], 10, 64)
	if err != nil {
		return false
	}
	issuedAt := time.Unix(issuedUnix, 0).UTC()
	now = now.UTC()
	if issuedAt.After(now.Add(5 * time.Minute)) {
		return false
	}
	if now.Sub(issuedAt) > ttl {
		return false
	}
	expectedToken := BoundSessionToken(rule, clientIP, userAgent, issuedAt)
	return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(strings.TrimSpace(token))) == 1
}

func FailureDomain(domain, pagePath string) string {
	return NormalizeDomain(domain) + "|page-password|" + CleanPath(pagePath)
}

func ParsePrefixLine(domain, rawLine string) (Rule, bool) {
	trimmedLine := strings.TrimSpace(rawLine)
	if trimmedLine == "" {
		return Rule{}, false
	}
	parts := strings.SplitN(trimmedLine, "\t", 2)
	normalizedDomain := NormalizeDomain(domain)
	if normalizedDomain == "" {
		normalizedDomain = "localhost"
	}
	rule := Rule{Domain: normalizedDomain, Path: CleanPath(parts[0])}
	if len(parts) == 2 {
		rule.PasswordHash = strings.TrimSpace(parts[1])
	}
	return rule, rule.Path != ""
}

func FindBestRule(domain, pagePath string, rules []Rule) (Rule, bool) {
	requestPath := CleanPath(pagePath)
	var bestRule Rule
	for _, candidateRule := range rules {
		candidateRule.Domain = NormalizeDomain(candidateRule.Domain)
		if candidateRule.Domain == "" {
			candidateRule.Domain = NormalizeDomain(domain)
		}
		candidateRule.Path = CleanPath(candidateRule.Path)
		candidateRule.PasswordHash = strings.TrimSpace(candidateRule.PasswordHash)
		if candidateRule.Path == "" || candidateRule.PasswordHash == "" {
			continue
		}
		if !HasProtectedPrefix(requestPath, candidateRule.Path) {
			continue
		}
		if len(candidateRule.Path) > len(bestRule.Path) {
			bestRule = candidateRule
		}
	}
	return bestRule, bestRule.Path != "" && bestRule.PasswordHash != ""
}

func FindBestRuleInPrefixData(domain, pagePath string, prefixData []byte) (Rule, bool) {
	normalizedDomain := NormalizeDomain(domain)
	if normalizedDomain == "" {
		normalizedDomain = "localhost"
	}
	rules := make([]Rule, 0)
	for _, rawLine := range strings.Split(string(prefixData), "\n") {
		rule, ok := ParsePrefixLine(normalizedDomain, rawLine)
		if ok {
			rules = append(rules, rule)
		}
	}
	return FindBestRule(normalizedDomain, pagePath, rules)
}

func PrefixFileBody(rules []Rule) []byte {
	lines := make([]string, 0, len(rules))
	for _, rule := range rules {
		rule.Path = CleanPath(rule.Path)
		rule.PasswordHash = strings.TrimSpace(rule.PasswordHash)
		if rule.Path == "" || rule.PasswordHash == "" {
			continue
		}
		lines = append(lines, rule.Path+"\t"+rule.PasswordHash)
	}
	if len(lines) == 0 {
		return nil
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func NormalizeDomain(domain string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(domain), "."))
}

func boundSessionSignature(rule Rule, clientIP string, issuedUnix int64) string {
	hashedBytes := sha256.Sum256([]byte(strings.Join([]string{
		"sitebrush page password session v2",
		NormalizeDomain(rule.Domain),
		CleanPath(rule.Path),
		strings.TrimSpace(rule.PasswordHash),
		strings.TrimSpace(clientIP),
		strconv.FormatInt(issuedUnix, 10),
	}, "\n")))
	return fmt.Sprintf("%x", hashedBytes)
}
