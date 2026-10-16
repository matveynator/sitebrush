from pathlib import Path


def function_span(text, name):
    markers = [f"func {name}(", f"func (a *App) {name}("]
    starts = [text.find(marker) for marker in markers]
    starts = [start for start in starts if start >= 0]
    if not starts:
        raise RuntimeError(f"function not found: {name}")
    start = min(starts)
    opening = text.find(" {\n", start)
    if opening < 0:
        raise RuntimeError(f"function body not found: {name}")
    opening += 1
    depth = 0
    quote = None
    escaped = False
    for index in range(opening, len(text)):
        ch = text[index]
        if quote:
            if quote != "`" and escaped:
                escaped = False
                continue
            if quote != "`" and ch == "\\":
                escaped = True
                continue
            if ch == quote:
                quote = None
            continue
        if ch in ('"', "'", "`"):
            quote = ch
            continue
        if ch == "{":
            depth += 1
        elif ch == "}":
            depth -= 1
            if depth == 0:
                return start, index + 1
    raise RuntimeError(f"unterminated function: {name}")


def replace_function(text, name, replacement):
    start, end = function_span(text, name)
    return text[:start] + replacement.rstrip() + text[end:]


def replace_once(text, old, new, label):
    count = text.count(old)
    if count != 1:
        raise RuntimeError(f"{label}: expected one match, got {count}")
    return text.replace(old, new, 1)


site_path = Path("sitebrush.go")
site = site_path.read_text()
if '"crypto/hmac"' not in site:
    site = replace_once(site, '\t"crypto/sha256"\n', '\t"crypto/hmac"\n\t"crypto/sha256"\n', "hmac import")

site = replace_function(site, "stableSiteID", r'''func stableSiteID(domain string) string {
    digest := hmac.New(sha256.New, []byte("sitebrush/site-id/v1"))
    _, _ = digest.Write([]byte(normalizeDomainName(domain)))
    sum := digest.Sum(nil)
    return hex.EncodeToString(sum[:16])
}''')

migrate_start, migrate_end = function_span(site, "migrateLegacyAnalytics")
migrate = site[migrate_start:migrate_end]
migrate = replace_once(
    migrate,
    '\tlegacyDirectory := filepath.Join(a.storageRootDir(), "analytics", "site-"+url.PathEscape(domain))\n\tlegacyPath := filepath.Join(legacyDirectory, browserstats.DatabaseName(domain))',
    '\tdomain = strings.TrimSpace(domain)\n\tsafeDomain := filepath.Base(domain)\n\tif domain == "" || strings.ContainsAny(domain, "\\x00\\r\\n/\\\\") || safeDomain != domain || safeDomain == "." || safeDomain == ".." {\n\t\treturn errors.New("unsafe legacy analytics domain")\n\t}\n\tlegacyDirectory := filepath.Join(a.storageRootDir(), "analytics", "site-"+url.PathEscape(safeDomain))\n\tlegacyPath := filepath.Join(legacyDirectory, browserstats.DatabaseName(safeDomain))',
    "legacy analytics path sanitizer",
)
site = site[:migrate_start] + migrate + site[migrate_end:]

site = replace_function(site, "collectAutomaticSSLDomainCandidates", r'''func (a *App) collectAutomaticSSLDomainCandidates(ctx context.Context, seenDomains map[string]struct{}) {
    collect := func(query string) {
        rows, err := a.db.QueryContext(ctx, query)
        if err != nil {
            return
        }
        defer rows.Close()
        for rows.Next() {
            var rawDomain sql.NullString
            if scanErr := rows.Scan(&rawDomain); scanErr != nil {
                continue
            }
            domain := normalizeDomainName(rawDomain.String)
            if domain == "" {
                continue
            }
            if _, seen := seenDomains[domain]; seen {
                continue
            }
            seenDomains[domain] = struct{}{}
        }
    }
    collect(`SELECT domain FROM pages`)
    collect(`SELECT domain FROM users
UNION SELECT domain FROM domain_states
UNION SELECT primary_domain FROM domain_aliases
UNION SELECT alias_domain FROM domain_aliases
UNION SELECT domain FROM domain_ssl_settings`)
}''')

site = replace_function(site, "domainIsAutomaticSSLCandidate", r'''func (a *App) domainIsAutomaticSSLCandidate(ctx context.Context, domain string) bool {
    certificateDomain := normalizeDomainName(domain)
    if certificateDomain == "" {
        return false
    }
    if a.autoCertCachedCertificateValid(certificateDomain, time.Now()) {
        return true
    }
    if a.pendingRegistrationDomain(ctx, certificateDomain) {
        return true
    }
    var domainCount int
    if err := a.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM pages WHERE domain=?`, certificateDomain).Scan(&domainCount); err == nil && domainCount > 0 {
        return true
    }
    query := `
SELECT COUNT(1) FROM (
SELECT domain FROM users WHERE domain=?
UNION SELECT domain FROM domain_states WHERE domain=?
UNION SELECT primary_domain FROM domain_aliases WHERE primary_domain=?
UNION SELECT alias_domain FROM domain_aliases WHERE alias_domain=?
UNION SELECT domain FROM domain_ssl_settings WHERE domain=?
)`
    err := a.db.QueryRowContext(ctx, query, certificateDomain, certificateDomain, certificateDomain, certificateDomain, certificateDomain).Scan(&domainCount)
    return err == nil && domainCount > 0
}''')

if "type aiIngressDecision struct" not in site:
    middleware = r'''type aiIngressDecision struct {
    machine bool
    owner   bool
}

type aiIngressDecisionContextKey struct{}

func requestWithAIIngressDecision(r *http.Request, machine, owner bool) *http.Request {
    decision := aiIngressDecision{machine: machine, owner: owner}
    return r.WithContext(context.WithValue(r.Context(), aiIngressDecisionContextKey{}, decision))
}

func (a *App) aiIngressDecisionForRequest(r *http.Request) (machine, owner bool) {
    if decision, ok := r.Context().Value(aiIngressDecisionContextKey{}).(aiIngressDecision); ok {
        return decision.machine, decision.owner
    }
    return a.aiIngressAdmission(r)
}

func (a *App) aiIngressMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        machine, owner := a.aiIngressAdmission(r)
        // Public logging, abuse detection and referrer cookies must observe the
        // same request as the public router, without hidden control selectors.
        admitted := aiRequestAfterAdmission(r, machine, owner)
        next.ServeHTTP(w, requestWithAIIngressDecision(admitted, machine, owner))
    })
}'''
    site = replace_function(site, "aiIngressMiddleware", middleware)

route_start, route_end = function_span(site, "route")
route = site[route_start:route_end]
route = replace_once(
    route,
    '\tmachine, owner := a.aiIngressAdmission(r)\n\tr = aiRequestAfterAdmission(r, machine, owner)',
    '\tmachine, owner := a.aiIngressDecisionForRequest(r)\n\tr = requestWithAIIngressDecision(aiRequestAfterAdmission(r, machine, owner), machine, owner)',
    "route ingress decision",
)
site = site[:route_start] + route + site[route_end:]

mcp_start, mcp_end = function_span(site, "siteMCPRoute")
mcp = site[mcp_start:mcp_end]
mcp = replace_once(mcp, '\tmachine, owner := a.aiIngressAdmission(r)', '\tmachine, owner := a.aiIngressDecisionForRequest(r)', "MCP ingress decision")
site = site[:mcp_start] + mcp + site[mcp_end:]
site_path.write_text(site)

store_path = Path("pkg/analytics/store.go")
store = store_path.read_text()
store_start, store_end = function_span(store, "storeDirectoryPath")
store_directory = store[store_start:store_end]
store_directory = replace_once(
    store_directory,
    'func storeDirectoryPath(root, domain string) (string, error) {\n\tif domain == "" || len(domain) > 253 || strings.ContainsAny(domain, "\\x00\\r\\n/\\\\") {',
    'func storeDirectoryPath(root, domain string) (string, error) {\n\tdomain = strings.TrimSpace(domain)\n\tsafeDomain := filepath.Base(domain)\n\tif domain == "" || len(domain) > 253 || strings.ContainsAny(domain, "\\x00\\r\\n/\\\\") || safeDomain != domain || safeDomain == "." || safeDomain == ".." {',
    "analytics directory validation",
)
store_directory = replace_once(store_directory, 'filepath.Join(root, "site-"+url.PathEscape(domain))', 'filepath.Join(root, "site-"+url.PathEscape(safeDomain))', "analytics safe directory component")
store = store[:store_start] + store_directory + store[store_end:]
store = replace_once(
    store,
    'func DatabaseName(domain string) string { return url.PathEscape(domain) + ".analytics.db" }',
    'func DatabaseName(domain string) string {\n\treturn url.PathEscape(filepath.Base(strings.TrimSpace(domain))) + ".analytics.db"\n}',
    "analytics database filename sanitizer",
)
store_path.write_text(store)

cross_path = Path("scripts/crosscompile/crosscompile.go")
cross = cross_path.read_text()
cross = replace_once(cross, '\t\t{goos: "netbsd", goarch: "amd64"},\n\t\t{goos: "netbsd", goarch: "arm64"},\n', '', "unsupported NetBSD server targets")
cross_path.write_text(cross)

cross_test_path = Path("scripts/crosscompile/crosscompile_test.go")
cross_test = cross_test_path.read_text()
cross_test = replace_once(cross_test, 'if len(targets) != 13 {\n\t\tt.Fatalf("expected 13 server-app targets, got %d", len(targets))\n\t}', 'if len(targets) != 11 {\n\t\tt.Fatalf("expected 11 server-app targets, got %d", len(targets))\n\t}', "server target count")
cross_test = replace_once(
    cross_test,
    '\tfoundNetBSD := false\n\tfor _, target := range targets {\n\t\tif target.goos == "netbsd" && target.goarch == "amd64" {\n\t\t\tfoundNetBSD = true\n\t\t\tbreak\n\t\t}\n\t}\n\tif !foundNetBSD {\n\t\tt.Fatal("server-app targets should include NetBSD")\n\t}\n',
    '\tfor _, target := range targets {\n\t\tif target.goos == "netbsd" {\n\t\t\tt.Fatal("server-app targets must exclude NetBSD while modernc.org/sqlite does not compile there")\n\t\t}\n\t}\n',
    "NetBSD target assertion",
)
cross_test_path.write_text(cross_test)

Path("sitebrush_pr193_regression_test.go").write_text(r'''package main

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestPR193PartitionedAutomaticSSLCandidatesQueryOwnersSeparately(t *testing.T) {
    application := newPartitionedTestApplication(t)

    contentDomain := "ssl-content.example"
    contentBoundary := contextWithSiteDatabaseCreation(contextWithDomain(context.Background(), contentDomain))
    if _, err := application.db.ExecContext(contentBoundary, `INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,0)`, contentDomain, "/", "Home", "content"); err != nil {
        t.Fatal(err)
    }
    seen := map[string]struct{}{}
    application.collectAutomaticSSLDomainCandidates(contentBoundary, seen)
    if _, ok := seen[contentDomain]; !ok {
        t.Fatal("automatic SSL candidate collection lost a content-owned domain")
    }
    if !application.domainIsAutomaticSSLCandidate(contentBoundary, contentDomain) {
        t.Fatal("automatic SSL admission rejected a content-owned domain")
    }

    controlDomain := "ssl-control.example"
    controlBoundary := contextWithSiteDatabaseCreation(contextWithDomain(context.Background(), controlDomain))
    if _, err := application.db.ExecContext(controlBoundary, `INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, controlDomain, "owner@example.org", "password"); err != nil {
        t.Fatal(err)
    }
    seen = map[string]struct{}{}
    application.collectAutomaticSSLDomainCandidates(controlBoundary, seen)
    if _, ok := seen[controlDomain]; !ok {
        t.Fatal("automatic SSL candidate collection lost a control-owned domain")
    }
    if !application.domainIsAutomaticSSLCandidate(controlBoundary, controlDomain) {
        t.Fatal("automatic SSL admission rejected a control-owned domain")
    }
}

func TestPR193AIIngressDecisionUsesRequestContext(t *testing.T) {
    application := &App{}
    request := httptest.NewRequest(http.MethodGet, "https://example.test/?mcp", nil)
    request = requestWithAIIngressDecision(request, true, false)
    machine, owner := application.aiIngressDecisionForRequest(request)
    if !machine || owner {
        t.Fatalf("cached ingress decision = machine %t owner %t, want true false", machine, owner)
    }
}
''')

Path("pkg/analytics/path_security_test.go").write_text(r'''package analytics

import (
    "path/filepath"
    "strings"
    "testing"
)

func TestStoreDirectoryPathRejectsTraversalComponents(t *testing.T) {
    root := t.TempDir()
    for _, domain := range []string{"../escape", "nested/domain", `nested\domain`, ".", ".."} {
        if _, err := storeDirectoryPath(root, domain); err == nil {
            t.Fatalf("storeDirectoryPath accepted unsafe domain %q", domain)
        }
    }
    directory, err := storeDirectoryPath(root, "example.com")
    if err != nil {
        t.Fatal(err)
    }
    relative, err := filepath.Rel(root, directory)
    if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
        t.Fatalf("analytics directory escaped root: %q (%v)", directory, err)
    }
}

func TestDatabaseNameIsSinglePathComponent(t *testing.T) {
    for _, domain := range []string{"example.com", "../escape", `..\escape`} {
        name := DatabaseName(domain)
        if name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
            t.Fatalf("DatabaseName(%q) produced unsafe component %q", domain, name)
        }
    }
}
''')
