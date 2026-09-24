package sitebrushtemplate

import (
	"crypto/sha256"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestTemplateSecurityBoundaryClassAndWrapperNormalization(t *testing.T) {
	for _, bad := range []string{"", "1bad", "bad space", "bad<script>", "bad/part"} {
		if validCSSClassName(bad) {
			t.Fatalf("SECURITY: unsafe automatic template class %q was accepted", bad)
		}
	}
	for _, good := range []string{"header", "header-2", "_private", "тема"} {
		if !validCSSClassName(good) {
			t.Fatalf("valid class %q was rejected", good)
		}
	}

	tags := explicitDocumentWrapperTags("<!doctype html><HTML><HEAD></HEAD><BODY><div></div></BODY></HTML>")
	for _, tag := range []string{"html", "head", "body"} {
		if _, ok := tags[tag]; !ok {
			t.Fatalf("explicit wrapper %q not detected", tag)
		}
	}
	if documentWrapperTag("div") {
		t.Fatal("ordinary element treated as document wrapper")
	}
}

func TestTemplateSecurityBoundaryIdentityClassesAreCanonicalized(t *testing.T) {
	attributes := []html.Attribute{
		{Key: "CLASS", Val: "selected SiteBrush-Template public public"},
		{Key: "data-x", Val: "  value  "},
		{Key: " ", Val: "ignored"},
	}
	got := normalizedAttributes(attributes)
	joined := strings.Join(got, "|")
	if strings.Contains(strings.ToLower(joined), "sitebrush-template") || strings.Contains(joined, "selected") {
		t.Fatalf("SECURITY: internal identity classes leaked into content fingerprint: %q", joined)
	}
	if strings.Count(joined, "public") != 1 || !strings.Contains(joined, "data-x=value") {
		t.Fatalf("normalized attributes = %q", joined)
	}

	merged := mergeClassNames("SiteBrush-Template PUBLIC selected", []string{"public", "NewClass", "sitebrush-template"})
	if strings.Count(strings.ToLower(merged), "sitebrush-template") != 1 || strings.Count(strings.ToLower(merged), "public") != 1 {
		t.Fatalf("template classes were duplicated: %q", merged)
	}
	if !classListHasSiteBrushTemplate(merged) {
		t.Fatalf("template marker missing after merge: %q", merged)
	}
	if strings.Contains(strings.ToLower(removeSiteBrushTemplateClass(merged)), "sitebrush-template") {
		t.Fatalf("template marker survived removal: %q", merged)
	}
}

func TestTemplateCSSNormalizationPreservesQuotedAttackLikeText(t *testing.T) {
	css := `a { content: "/* not a comment */ </style><script>"; color: red; } /* real comment */`
	normalized := normalizedCSS(css)
	if !strings.Contains(normalized, "/* not a comment */ </style><script>") {
		t.Fatalf("quoted CSS content changed unexpectedly: %q", normalized)
	}
	if strings.Contains(normalized, "real comment") {
		t.Fatalf("CSS comment survived normalization: %q", normalized)
	}
	if !cssWhitespaceIsSignificant('a', 'b') || cssWhitespaceIsSignificant(':', 'b') || cssWhitespaceIsSignificant('a', '}') {
		t.Fatal("CSS whitespace significance classification failed")
	}
}

func TestAutomaticTemplatePurposeAndIdentifierCollisionBranches(t *testing.T) {
	for _, tc := range []struct {
		node *html.Node
		want string
	}{
		{nil, "block"},
		{&html.Node{Type: html.ElementNode, Data: "HEADER"}, "header"},
		{&html.Node{Type: html.ElementNode, Data: "div", Attr: []html.Attribute{{Key: "role", Val: "navigation"}}}, "nav"},
		{&html.Node{Type: html.ElementNode, Data: "div", Attr: []html.Attribute{{Key: "role", Val: "complementary"}}}, "sidebar"},
	} {
		if got := automaticTemplatePurpose(tc.node); got != tc.want {
			t.Fatalf("purpose = %q, want %q", got, tc.want)
		}
	}

	hash := sha256.Sum256([]byte("same-template"))
	group := &templateCandidateGroup{
		hash: hash,
		occurrences: []*templateCandidate{{node: &html.Node{Type: html.ElementNode, Data: "footer"}}},
	}
	first := automaticTemplateIdentifier(group, map[string]struct{}{})
	used := map[string]struct{}{strings.ToLower(strings.TrimPrefix(first, "sitebrush-template-")): {}}
	second := automaticTemplateIdentifier(group, used)
	if first == second {
		t.Fatalf("identifier collision was not resolved: %q", first)
	}
}

func TestDetectionProgressAndCandidateBoundaryBranches(t *testing.T) {
	if got := detectionPhasePercent(10, 80, 5, 0); got != 80 {
		t.Fatalf("zero-total progress = %d", got)
	}
	if got := detectionPhasePercent(10, 80, 5, 10); got != 45 {
		t.Fatalf("phase progress = %d", got)
	}

	called := 0
	reportDetectionProgress(func(value int) { called = value }, 73)
	if called != 73 {
		t.Fatalf("progress callback = %d", called)
	}
	reportDetectionProgress(nil, 10)

	fp := subtreeFingerprint{elementCount: AutomaticTemplateMinimumElementCount, canonicalBytes: AutomaticTemplateMinimumCanonicalBytes}
	node := &html.Node{Type: html.ElementNode, Data: "div"}
	if !automaticTemplateCandidateAllowed(node, fp, nil) {
		t.Fatal("valid ordinary candidate rejected")
	}
	fp.containsExistingTemplate = true
	if automaticTemplateCandidateAllowed(node, fp, nil) {
		t.Fatal("SECURITY: nested existing template was accepted as automatic candidate")
	}
}

func TestTemplateRewriteAndMatchHelperBranches(t *testing.T) {
	rewrite := classRewrite{classNames: []string{"shared", "theme"}}
	tests := []struct {
		input string
		remove bool
		want string
	}{
		{"<div>", false, "<div class=\"SiteBrush-Template shared theme\">"},
		{"<img />", false, "<img  class=\"SiteBrush-Template shared theme\"/>"},
		{"<div class=\"plain\">", false, "<div class=\"SiteBrush-Template shared theme plain\">"},
		{"<div class='SiteBrush-Template plain'>", true, "<div class=\"plain\">"},
		{"<div class=\"SiteBrush-Template\">", true, "<div>"},
		{"broken", false, "broken"},
	}
	for _, tc := range tests {
		got := rewriteClassStartTag(tc.input, rewrite, tc.remove)
		// The rewriter preserves harmless source whitespace around attributes.
		// Compare normalized markup so this test protects class semantics rather
		// than depending on an insignificant formatting detail.
		if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(tc.want), " ") {
			t.Fatalf("rewrite %q remove=%v = %q, want semantic equivalent of %q", tc.input, tc.remove, got, tc.want)
		}
	}

	matches := []match{
		{start: 5, end: 10, id: "later"},
		{start: 0, end: 20, id: "outer"},
		{start: 0, end: 5, id: "short"},
		{start: 21, end: 30, id: "next"},
	}
	filtered := sortedNonOverlappingMatches(matches)
	if len(filtered) != 2 || filtered[0].id != "outer" || filtered[1].id != "next" {
		t.Fatalf("non-overlapping matches = %#v", filtered)
	}
	if got := sortedNonOverlappingMatches([]match{{start: 1, end: 2}}); len(got) != 1 {
		t.Fatalf("single match changed: %#v", got)
	}
}

func TestTemplateIdentifierAndNodeHelperBranches(t *testing.T) {
	node := &html.Node{Type: html.ElementNode, Data: "div", Attr: []html.Attribute{{Key: "CLASS", Val: "plain SiteBrush-Template sitebrush-template-header-abc"}}}
	if !nodeHasSiteBrushTemplate(node) || nodeClassValue(node) == "" {
		t.Fatal("existing template node was not recognized")
	}
	if nodeHasSiteBrushTemplate(nil) || nodeClassValue(nil) != "" {
		t.Fatal("nil node unexpectedly contains template metadata")
	}

	identifier := templateIdentifierFromAttributes("DIV", node.Attr)
	if identifier != "div\x00header-abc" {
		t.Fatalf("template identifier = %q", identifier)
	}
	if identifier := templateIdentifierFromAttributes("div", []html.Attribute{{Key: "id", Val: "x"}}); identifier != "" {
		t.Fatalf("identifier without class = %q", identifier)
	}

	elements := []classElement{
		{matchKey: "same", hasTemplate: false, classNames: []string{"plain"}},
		{matchKey: "same", hasTemplate: true, classNames: []string{"SiteBrush-Template", "shared"}},
	}
	if got := strings.Join(templateClassNamesForKey("same", elements), " "); got != "SiteBrush-Template shared" {
		t.Fatalf("template class names = %q", got)
	}
	if got := templateClassNamesForKey("missing", elements); len(got) != 1 || got[0] != "SiteBrush-Template" {
		t.Fatalf("default template classes = %#v", got)
	}

	withoutClass := &html.Node{Type: html.ElementNode, Data: "footer"}
	addAutomaticTemplateClasses(withoutClass, "sitebrush-template-footer-1")
	if !nodeHasSiteBrushTemplate(withoutClass) {
		t.Fatalf("automatic class was not added: %#v", withoutClass.Attr)
	}
	addAutomaticTemplateClasses(node, "sitebrush-template-new")
	if !strings.Contains(nodeClassValue(node), "sitebrush-template-new") {
		t.Fatalf("automatic class was not prepended: %q", nodeClassValue(node))
	}
}
