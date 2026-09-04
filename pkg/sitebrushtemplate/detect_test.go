package sitebrushtemplate

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestDetectAutomaticTemplatesMatchesLargeHeaderAcrossFormattingAndTagCase(t *testing.T) {
	headerBody := automaticTemplateTestElementList("Welcome")
	pageList := []DetectionPage{
		{Key: "/", HTML: "<!doctype html><HTML><body><HEADER class=\"main-header\" data-kind=\"Primary\">\n" + headerBody + "\n</HEADER><main>Home</main></body></HTML>"},
		{Key: "/about", HTML: "<!doctype html><html><body><header DATA-KIND=\"Primary\" CLASS=\"main-header\">" + strings.ReplaceAll(headerBody, "><", "> \n <") + "</header><main>About</main></body></html>"},
	}

	detectedPages, err := DetectAutomaticTemplates(pageList)
	if err != nil {
		t.Fatal(err)
	}
	firstIdentifier := firstAutomaticTemplateIdentifier(t, detectedPages[0].HTML, "header")
	secondIdentifier := firstAutomaticTemplateIdentifier(t, detectedPages[1].HTML, "header")
	if firstIdentifier == "" || firstIdentifier != secondIdentifier {
		t.Fatalf("expected one shared header identifier, got %q and %q", firstIdentifier, secondIdentifier)
	}
	if !strings.HasPrefix(firstIdentifier, "sitebrush-template-header-") || !validCSSClassName(firstIdentifier) {
		t.Fatalf("expected a readable valid CSS class identifier, got %q", firstIdentifier)
	}
	if templateCount(detectedPages[0].HTML) != 1 || templateCount(detectedPages[1].HTML) != 1 {
		t.Fatalf("expected only the largest repeated header to be marked: %#v", detectedPages)
	}
	reversedPages, err := DetectAutomaticTemplates([]DetectionPage{pageList[1], pageList[0]})
	if err != nil {
		t.Fatal(err)
	}
	if reversedIdentifier := firstAutomaticTemplateIdentifier(t, reversedPages[0].HTML, "header"); reversedIdentifier != firstIdentifier {
		t.Fatalf("identifier changed with page order: %q, want %q", reversedIdentifier, firstIdentifier)
	}
}

func TestDetectAutomaticTemplatesReportsMonotonicProgress(t *testing.T) {
	progressList := make([]int, 0)
	_, err := DetectAutomaticTemplatesWithProgress([]DetectionPage{
		{Key: "/", HTML: `<main><p>same</p></main>`},
		{Key: "/two", HTML: `<main><p>same</p></main>`},
	}, func(completedPercent int) {
		progressList = append(progressList, completedPercent)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(progressList) < 2 || progressList[0] != 0 || progressList[len(progressList)-1] != 100 {
		t.Fatalf("progress endpoints = %#v", progressList)
	}
	for progressIndex := 1; progressIndex < len(progressList); progressIndex++ {
		if progressList[progressIndex] < progressList[progressIndex-1] {
			t.Fatalf("progress is not monotonic: %#v", progressList)
		}
	}
}

func TestDetectAutomaticTemplatesDoesNotMergeDifferentContent(t *testing.T) {
	pageList := make([]DetectionPage, 0, 100)
	for pageIndex := 0; pageIndex < 100; pageIndex++ {
		headerText := "Alpha"
		if pageIndex == 1 {
			headerText = "alpha"
		}
		pageList = append(pageList, DetectionPage{
			Key:  fmt.Sprintf("/%d", pageIndex),
			HTML: "<html><body><header data-path=\"/CaseSensitive\">" + automaticTemplateTestElementList(headerText) + "</header></body></html>",
		})
	}

	detectedPages, err := DetectAutomaticTemplates(pageList[:2])
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range detectedPages {
		if templateCount(page.HTML) != 0 {
			t.Fatalf("different user text must not be merged: %s", page.HTML)
		}
	}
	attributePages, err := DetectAutomaticTemplates([]DetectionPage{
		{Key: "/attribute-1", HTML: `<html><body><header data-path="/CaseSensitive">` + automaticTemplateTestElementList("Same") + `</header></body></html>`},
		{Key: "/attribute-2", HTML: `<html><body><header data-path="/casesensitive">` + automaticTemplateTestElementList("Same") + `</header></body></html>`},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range attributePages {
		if firstAutomaticTemplateIdentifier(t, page.HTML, "header") != "" {
			t.Fatalf("headers with case-sensitive attribute differences must not be merged: %s", page.HTML)
		}
	}

	javaScriptPages, err := DetectAutomaticTemplates([]DetectionPage{
		{Key: "/script-1", HTML: `<html data-page="1"><head></head><body><script>window.mode = "one";</script></body></html>`},
		{Key: "/script-2", HTML: `<html data-page="2"><head></head><body><script>window.mode = "two";</script></body></html>`},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range javaScriptPages {
		if firstAutomaticTemplateIdentifier(t, page.HTML, "script") != "" {
			t.Fatalf("different JavaScript must not be merged: %s", page.HTML)
		}
	}
}

func TestDetectAutomaticTemplatesFindsAnyRepeatedElement(t *testing.T) {
	testCases := []struct {
		name       string
		tagName    string
		firstHTML  string
		secondHTML string
	}{
		{name: "image", tagName: "img", firstHTML: `<body data-page="1"><img src="/logo.png" alt="Logo"></body>`, secondHTML: `<body data-page="2"><img src="/logo.png" alt="Logo"></body>`},
		{name: "link", tagName: "a", firstHTML: `<body data-page="1"><a href="/help">Help</a></body>`, secondHTML: `<body data-page="2"><a href="/help">Help</a></body>`},
		{name: "button", tagName: "button", firstHTML: `<body data-page="1"><button type="button">Open</button></body>`, secondHTML: `<body data-page="2"><button type="button">Open</button></body>`},
		{name: "input", tagName: "input", firstHTML: `<body data-page="1"><input name="search" type="search"></body>`, secondHTML: `<body data-page="2"><input name="search" type="search"></body>`},
		{name: "table row", tagName: "tr", firstHTML: `<body data-page="1"><table data-page="1"><tbody data-page="1"><tr><td>Shared</td></tr></tbody></table></body>`, secondHTML: `<body data-page="2"><table data-page="2"><tbody data-page="2"><tr><td>Shared</td></tr></tbody></table></body>`},
		{name: "style", tagName: "style", firstHTML: `<head data-page="1"><style>.shared { color: red; }</style></head><body data-page="1"></body>`, secondHTML: `<head data-page="2"><style>.shared{color:red}</style></head><body data-page="2"></body>`},
		{name: "inline JavaScript", tagName: "script", firstHTML: `<head data-page="1"><script>window.shared = true;</script></head><body data-page="1"></body>`, secondHTML: `<head data-page="2"><script>window.shared = true;</script></head><body data-page="2"></body>`},
		{name: "external JavaScript", tagName: "script", firstHTML: `<head data-page="1"><script src="/shared.js"></script></head><body data-page="1"></body>`, secondHTML: `<head data-page="2"><script src="/shared.js"></script></head><body data-page="2"></body>`},
		{name: "external CSS", tagName: "link", firstHTML: `<head data-page="1"><link rel="stylesheet" href="/shared.css"></head><body data-page="1"></body>`, secondHTML: `<head data-page="2"><link rel="stylesheet" href="/shared.css"></head><body data-page="2"></body>`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			detectedPages, err := DetectAutomaticTemplates([]DetectionPage{
				{Key: "/first", HTML: `<html data-page="1">` + testCase.firstHTML + `</html>`},
				{Key: "/second", HTML: `<html data-page="2">` + testCase.secondHTML + `</html>`},
			})
			if err != nil {
				t.Fatal(err)
			}
			firstIdentifier := firstAutomaticTemplateIdentifier(t, detectedPages[0].HTML, testCase.tagName)
			secondIdentifier := firstAutomaticTemplateIdentifier(t, detectedPages[1].HTML, testCase.tagName)
			if firstIdentifier == "" || firstIdentifier != secondIdentifier {
				t.Fatalf("expected one shared %s identifier, got %q and %q", testCase.tagName, firstIdentifier, secondIdentifier)
			}
		})
	}
}

func TestVoidTemplatesPropagateFromOrdinaryStartTags(t *testing.T) {
	testCases := []struct {
		name       string
		sourceHTML string
		targetHTML string
		expected   string
	}{
		{name: "image", sourceHTML: `<img class="SiteBrush-Template sitebrush-template-shared" src="/updated.png">`, targetHTML: `<img class="SiteBrush-Template sitebrush-template-shared" src="/old.png">`, expected: `/updated.png`},
		{name: "input", sourceHTML: `<input class="SiteBrush-Template sitebrush-template-shared" value="updated">`, targetHTML: `<input class="SiteBrush-Template sitebrush-template-shared" value="old">`, expected: `value="updated"`},
		{name: "stylesheet", sourceHTML: `<link class="SiteBrush-Template sitebrush-template-shared" rel="stylesheet" href="/updated.css">`, targetHTML: `<link class="SiteBrush-Template sitebrush-template-shared" rel="stylesheet" href="/old.css">`, expected: `/updated.css`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			updatedHTML, changed := ReplaceBlocks(testCase.targetHTML, ExtractBlocks(testCase.sourceHTML))
			if !changed || !strings.Contains(updatedHTML, testCase.expected) {
				t.Fatalf("ordinary void start tag was not propagated: %s", updatedHTML)
			}
		})
	}
}

func TestDetectAutomaticTemplatesHandlesExplicitAndSyntheticDocumentWrappers(t *testing.T) {
	explicitPages, err := DetectAutomaticTemplates([]DetectionPage{
		{Key: "/first", HTML: `<html><head></head><body>Same</body></html>`},
		{Key: "/second", HTML: `<html><head></head><body>Same</body></html>`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstAutomaticTemplateIdentifier(t, explicitPages[0].HTML, "html") == "" {
		t.Fatal("explicit repeated html element must be eligible")
	}

	syntheticPages, err := DetectAutomaticTemplates([]DetectionPage{
		{Key: "/first", HTML: `<div data-page="1"><button>Shared</button></div>`},
		{Key: "/second", HTML: `<div data-page="2"><button>Shared</button></div>`},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range syntheticPages {
		for _, tagName := range []string{"html", "head", "body"} {
			if firstAutomaticTemplateIdentifier(t, page.HTML, tagName) != "" {
				t.Fatalf("synthetic %s must not become a template: %s", tagName, page.HTML)
			}
		}
		if firstAutomaticTemplateIdentifier(t, page.HTML, "button") == "" {
			t.Fatalf("real repeated button was not detected: %s", page.HTML)
		}
	}
}

func TestDetectAutomaticTemplatesRejectsNestedGroupWithStandaloneOccurrence(t *testing.T) {
	component := `<section class="feature">` + automaticTemplateTestElementList("Shared") + `</section>`
	header := `<header><div class="brand">Brand</div>` + component + `<nav>` + automaticTemplateTestElementList("Nav") + `</nav></header>`
	pageList := []DetectionPage{
		{Key: "/", HTML: `<html data-page="1"><body data-page="1">` + header + `</body></html>`},
		{Key: "/about", HTML: `<html data-page="2"><body data-page="2">` + header + `</body></html>`},
		{Key: "/feature", HTML: `<html data-page="3"><body data-page="3"><main>` + component + `</main></body></html>`},
	}

	detectedPages, err := DetectAutomaticTemplates(pageList)
	if err != nil {
		t.Fatal(err)
	}
	headerIdentifier := firstAutomaticTemplateIdentifier(t, detectedPages[0].HTML, "header")
	if headerIdentifier == "" || headerIdentifier != firstAutomaticTemplateIdentifier(t, detectedPages[1].HTML, "header") {
		t.Fatal("expected the largest common header to be selected")
	}
	componentIdentifier := firstAutomaticTemplateIdentifier(t, detectedPages[2].HTML, "section")
	if componentIdentifier != "" {
		t.Fatalf("nested component must not receive a propagation-incompatible template: %q", componentIdentifier)
	}
	if templateCount(detectedPages[0].HTML) != 1 || templateCount(detectedPages[1].HTML) != 1 || templateCount(detectedPages[2].HTML) != 0 {
		t.Fatalf("expected only the two outer headers to be marked: %#v", detectedPages)
	}
}

func TestDetectAutomaticTemplatesRejectsDuplicateOccurrencesWithinPage(t *testing.T) {
	component := `<section class="feature">` + automaticTemplateTestElementList("Repeated") + `</section>`
	detectedPages, err := DetectAutomaticTemplates([]DetectionPage{
		{Key: "/", HTML: `<html><body><main>` + component + component + `</main></body></html>`},
		{Key: "/about", HTML: `<html><body><main>` + component + component + `</main></body></html>`},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range detectedPages {
		if firstAutomaticTemplateIdentifier(t, page.HTML, "section") != "" {
			t.Fatalf("ambiguous same-page sections must not share an identifier: %s", page.HTML)
		}
	}
}

func TestDetectAutomaticTemplatesPreservesSignificantTextWhitespace(t *testing.T) {
	testCases := []struct {
		name       string
		firstText  string
		secondText string
	}{
		{name: "non-breaking space", firstText: "A B", secondText: "A&nbsp;B"},
		{name: "repeated ordinary space", firstText: "A B", secondText: "A  B"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			detectedPages, err := DetectAutomaticTemplates([]DetectionPage{
				{Key: "/first", HTML: `<html><body><header>` + automaticTemplateTestElementList(testCase.firstText) + `</header></body></html>`},
				{Key: "/second", HTML: `<html><body><header>` + automaticTemplateTestElementList(testCase.secondText) + `</header></body></html>`},
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, page := range detectedPages {
				if templateCount(page.HTML) != 0 {
					t.Fatalf("significant text whitespace must prevent template matching: %s", page.HTML)
				}
			}
		})
	}
}

func TestDetectAutomaticTemplatesNormalizesStyleWithoutMergingDifferentCSS(t *testing.T) {
	padding := strings.Repeat(".shared-item { margin: 0; padding: 1px; }\n", 5)
	pageList := []DetectionPage{
		{Key: "/", HTML: `<html><head data-page="1"><style>body { color: red; } ` + padding + `</style></head><body>Home</body></html>`},
		{Key: "/about", HTML: `<html><head data-page="2"><style>body{color:red}` + strings.ReplaceAll(padding, " ", "") + `</style></head><body>About</body></html>`},
		{Key: "/other", HTML: `<html><head data-page="3"><style>body{color:blue}` + strings.ReplaceAll(padding, " ", "") + `</style></head><body>Other</body></html>`},
	}

	detectedPages, err := DetectAutomaticTemplates(pageList)
	if err != nil {
		t.Fatal(err)
	}
	firstIdentifier := firstAutomaticTemplateIdentifier(t, detectedPages[0].HTML, "style")
	secondIdentifier := firstAutomaticTemplateIdentifier(t, detectedPages[1].HTML, "style")
	if firstIdentifier == "" || firstIdentifier != secondIdentifier {
		t.Fatalf("expected formatting-only CSS differences to match, got %q and %q", firstIdentifier, secondIdentifier)
	}
	if firstAutomaticTemplateIdentifier(t, detectedPages[2].HTML, "style") != "" {
		t.Fatal("different CSS declaration values must not be merged")
	}
}

func TestDetectAutomaticTemplatesFindsSharedFooter(t *testing.T) {
	footer := `<footer>` + automaticTemplateTestElementList("Contact") + `</footer>`
	detectedPages, err := DetectAutomaticTemplates([]DetectionPage{
		{Key: "/", HTML: `<html><body><main>Home</main>` + footer + `</body></html>`},
		{Key: "/about", HTML: `<html><body><main>About</main>` + footer + `</body></html>`},
	})
	if err != nil {
		t.Fatal(err)
	}
	identifier := firstAutomaticTemplateIdentifier(t, detectedPages[0].HTML, "footer")
	if identifier == "" || identifier != firstAutomaticTemplateIdentifier(t, detectedPages[1].HTML, "footer") {
		t.Fatalf("expected one shared footer identifier: %#v", detectedPages)
	}
}

func TestDetectAutomaticTemplatesPreservesExistingMarkupAndAvoidsIdentifierCollision(t *testing.T) {
	sharedHeader := `<header>` + automaticTemplateTestElementList("Shared") + `</header>`
	initialDetection, err := DetectAutomaticTemplates([]DetectionPage{
		{Key: "/initial-1", HTML: `<html><body>` + sharedHeader + `</body></html>`},
		{Key: "/initial-2", HTML: `<html><body>` + sharedHeader + `</body></html>`},
	})
	if err != nil {
		t.Fatal(err)
	}
	collidingIdentifier := firstAutomaticTemplateIdentifier(t, initialDetection[0].HTML, "header")
	existingClassValue := "SiteBrush-Template " + collidingIdentifier + " custom"
	existing := `<footer class="` + existingClassValue + `">` + automaticTemplateTestElementList("Existing") + `</footer>`
	pageList := []DetectionPage{
		{Key: "/", HTML: `<html><body>` + existing + sharedHeader + `</body></html>`},
		{Key: "/about", HTML: `<html><body>` + existing + sharedHeader + `</body></html>`},
	}

	detectedPages, err := DetectAutomaticTemplates(pageList)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range detectedPages {
		if !strings.Contains(page.HTML, existingClassValue) {
			t.Fatalf("existing SiteBrush-Template markup changed: %s", page.HTML)
		}
		if templateCount(page.HTML) != 2 {
			t.Fatalf("expected existing footer plus detected header, got %s", page.HTML)
		}
	}
	if firstAutomaticTemplateIdentifier(t, detectedPages[0].HTML, "header") == collidingIdentifier {
		t.Fatal("automatic identifier collided with existing markup")
	}
}

func automaticTemplateTestElementList(text string) string {
	var elements strings.Builder
	for elementIndex := 0; elementIndex < 9; elementIndex++ {
		fmt.Fprintf(&elements, `<div data-index="%d"><span>%s %d</span></div>`, elementIndex, text, elementIndex)
	}
	return elements.String()
}

func firstAutomaticTemplateIdentifier(t *testing.T, sourceHTML, tagName string) string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(sourceHTML))
	if err != nil {
		t.Fatal(err)
	}
	for node := root; node != nil; node = nextDOMNode(root, node) {
		if node.Type != html.ElementNode || !strings.EqualFold(node.Data, tagName) {
			continue
		}
		for _, className := range strings.Fields(nodeClassValue(node)) {
			if strings.HasPrefix(className, "sitebrush-template-") {
				return className
			}
		}
	}
	return ""
}

func templateCount(sourceHTML string) int {
	return strings.Count(sourceHTML, "SiteBrush-Template")
}
