package httpsecurity

import (
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// LocalRedirectTarget reduces every application redirect to a path on the current origin.
func LocalRedirectTarget(rawTarget string, fallback string) string {
	fallbackTarget := localRedirectTargetOrRoot(fallback)
	target, ok := parseLocalRedirectTarget(rawTarget)
	if !ok {
		return fallbackTarget
	}
	return target
}

func localRedirectTargetOrRoot(rawTarget string) string {
	target, ok := parseLocalRedirectTarget(rawTarget)
	if !ok {
		return "/"
	}
	return target
}

func parseLocalRedirectTarget(rawTarget string) (string, bool) {
	trimmedTarget := strings.TrimSpace(rawTarget)
	if trimmedTarget == "" || strings.ContainsAny(trimmedTarget, "\r\n\\") {
		return "", false
	}
	parsedTarget, err := url.Parse(trimmedTarget)
	if err != nil || parsedTarget.IsAbs() || parsedTarget.Host != "" || parsedTarget.User != nil || parsedTarget.Opaque != "" {
		return "", false
	}
	decodedPath, err := url.PathUnescape(parsedTarget.EscapedPath())
	if err != nil || !strings.HasPrefix(decodedPath, "/") || strings.HasPrefix(decodedPath, "//") || strings.Contains(decodedPath, "\\") {
		return "", false
	}
	cleanedPath := path.Clean(decodedPath)
	if cleanedPath == "." || cleanedPath == "" {
		cleanedPath = "/"
	}
	if strings.HasSuffix(decodedPath, "/") && cleanedPath != "/" {
		cleanedPath += "/"
	}
	safeTarget := &url.URL{Path: cleanedPath, RawQuery: parsedTarget.RawQuery, Fragment: parsedTarget.Fragment}
	return safeTarget.String(), true
}

// RedirectLocal is the only application boundary for redirects within the current site.
func RedirectLocal(w http.ResponseWriter, r *http.Request, rawTarget string, statusCode int) {
	http.Redirect(w, r, LocalRedirectTarget(rawTarget, "/"), statusCode)
}

// RedirectExternal requires callers to pass an explicit absolute HTTP URL.
func RedirectExternal(w http.ResponseWriter, r *http.Request, target *url.URL, statusCode int) {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil || strings.ContainsAny(target.String(), "\r\n") {
		RedirectLocal(w, r, "/", statusCode)
		return
	}
	http.Redirect(w, r, target.String(), statusCode)
}

// RedirectHTTPS upgrades a public request without accepting a host from any other source.
func RedirectHTTPS(w http.ResponseWriter, r *http.Request, statusCode int) {
	if r == nil || r.URL == nil || IsLocalRequest(r) {
		return
	}
	target := *r.URL
	target.Scheme = "https"
	target.Host = r.Host
	RedirectExternal(w, r, &target, statusCode)
}

// UsesHTTPS reads transport state and the conventional proxy boundary header.
func UsesHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	forwardedProtocol := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if separatorIndex := strings.Index(forwardedProtocol, ","); separatorIndex >= 0 {
		forwardedProtocol = forwardedProtocol[:separatorIndex]
	}
	return strings.EqualFold(strings.TrimSpace(forwardedProtocol), "https")
}

// IsLocalRequest identifies the only hosts allowed to use sensitive cookies over HTTP.
func IsLocalRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	host := strings.TrimSpace(r.Host)
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// SetSensitiveCookie applies one policy to every authentication and challenge cookie.
func SetSensitiveCookie(w http.ResponseWriter, r *http.Request, cookie *http.Cookie) {
	if cookie == nil {
		return
	}
	cookie.Path = "/"
	cookie.HttpOnly = true
	cookie.SameSite = http.SameSiteLaxMode
	cookie.Secure = !IsLocalRequest(r)
	http.SetCookie(w, cookie)
}
