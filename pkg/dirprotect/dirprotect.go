package dirprotect

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"hash/fnv"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
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

func Hash(password string) (string, error) {
	hashedBytes, err := bcrypt.GenerateFromPassword(passwordPrehash(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash page password: %w", err)
	}
	return string(hashedBytes), nil
}

func Matches(storedHash, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(strings.TrimSpace(storedHash)), passwordPrehash(password))
	return err == nil
}

func passwordPrehash(password string) []byte {
	prehash := hmac.New(sha256.New, []byte("sitebrush page password bcrypt prehash v1"))
	_, _ = prehash.Write([]byte(password))
	return prehash.Sum(nil)
}

func CookieName(domain, pagePath string) string {
	cookieIdentifier := fnv.New64a()
	_, _ = cookieIdentifier.Write([]byte(NormalizeDomain(domain) + "\n" + CleanPath(pagePath)))
	return "sitebrush_page_password_" + fmt.Sprintf("%016x", cookieIdentifier.Sum64())
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
	return hmac.Equal([]byte(expectedToken), []byte(strings.TrimSpace(token)))
}

func FailureDomain(domain, pagePath string) string {
	return NormalizeDomain(domain) + "|page-password|" + CleanPath(pagePath)
}

func FailureDomainPrefix(domain string) string {
	normalizedDomain := NormalizeDomain(domain)
	if normalizedDomain == "" {
		normalizedDomain = "localhost"
	}
	return normalizedDomain + "|page-password|"
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
	signature := hmac.New(sha256.New, []byte(strings.TrimSpace(rule.PasswordHash)))
	_, _ = signature.Write([]byte(strings.Join([]string{
		"sitebrush page password session v2",
		NormalizeDomain(rule.Domain),
		CleanPath(rule.Path),
		strings.TrimSpace(clientIP),
		strconv.FormatInt(issuedUnix, 10),
	}, "\n")))
	return fmt.Sprintf("%x", signature.Sum(nil))
}
