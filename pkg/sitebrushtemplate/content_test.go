package sitebrushtemplate

import (
	"strings"
	"testing"
)

func TestAutomaticTemplatesRejectContentlessAndUnpairedSource(t *testing.T) {
	for _, sourceHTML := range []string{
		`<meta name="generator" content="Site Kit by Google 1.188.0"/>`,
		`<img src="/logo.png">`, `<input value="Search">`, `<link rel="stylesheet" href="/main.css">`,
		`<br><hr><wbr><source src="/movie.mp4">`,
		`<div class="column"><div><span> </span></div></div>`,
		`<table><tbody><tr><td>&nbsp;</td><td></td></tr></tbody></table>`,
		"<div> \n\t&nbsp;\u200b\ufeff<!-- caption --></div>",
		`<svg></svg>`, `<svg><path d=""/></svg>`,
		`<path d="M0 0L10 10"></path>`,
		`<style> /* empty */ </style>`, `<script> /* empty */ // empty` + "\n</script>",
		"<script><!-- empty\n// empty\n--></script>",
		"<script>#!/usr/bin/env node\n// empty</script>",
		`<script src="/shared.js"></script>`,
		`<div>Unclosed`, `<div><span>Unclosed</div>`, `<div>Broken</span></div>`,
		`<div/><span>Unclosed`, `<div/>`,
	} {
		t.Run(sourceHTML, func(t *testing.T) {
			pages := []DetectionPage{{Key: "/one", HTML: sourceHTML}, {Key: "/two", HTML: sourceHTML}}
			detectedPages, err := DetectAutomaticTemplates(pages)
			if err != nil {
				t.Fatal(err)
			}
			for _, page := range detectedPages {
				if page.HTML != sourceHTML {
					t.Fatalf("ineligible source was changed: %s", page.HTML)
				}
			}
		})
	}
}

func TestAutomaticTemplatesPreserveRealAttributes(t *testing.T) {
	for _, openingTag := range []string{
		`<DIV title='literal class="pretend"' CLASS='First second'>`,
		`<DIV title='literal class="unterminated' CLASS="First second">`,
		`<DIV title='literal class="pretend"' CLASS=First>`,
		`<DIV title='literal class="pretend"'>`,
		`<DIV CLASS='First&quot;second'>`,
		`<DIV class>`, `<DIV class=>`, `<DIV class = >`, `<DIV class="">`,
		`<DIV class-name="untouched">`,
	} {
		t.Run(openingTag, func(t *testing.T) {
			sourceHTML := openingTag + "Text</DIV>"
			pages, err := DetectAutomaticTemplates([]DetectionPage{{Key: "/one", HTML: sourceHTML}, {Key: "/two", HTML: sourceHTML}})
			if err != nil {
				t.Fatal(err)
			}
			originalRoot, _, err := parseDetectionSource(sourceHTML)
			if err != nil {
				t.Fatal(err)
			}
			updatedRoot, _, err := parseDetectionSource(pages[0].HTML)
			if err != nil {
				t.Fatal(err)
			}
			originalNode := originalRoot.FirstChild
			updatedNode := updatedRoot.FirstChild
			if !nodeHasSiteBrushTemplate(updatedNode) {
				t.Fatalf("actual class was not marked: %s", pages[0].HTML)
			}
			for _, attribute := range originalNode.Attr {
				if strings.EqualFold(attribute.Key, "class") {
					if !strings.HasSuffix(nodeClassValue(updatedNode), attribute.Val) {
						t.Fatalf("existing class changed: %s", pages[0].HTML)
					}
					continue
				}
				found := false
				for _, updatedAttribute := range updatedNode.Attr {
					if updatedAttribute == attribute {
						found = true
					}
				}
				if !found {
					t.Fatalf("attribute changed: %s", pages[0].HTML)
				}
			}
			if !strings.HasSuffix(pages[0].HTML, ">Text</DIV>") {
				t.Fatalf("inner source changed: %s", pages[0].HTML)
			}
		})
	}
}

func TestAutomaticTemplatesFindContentAndPreserveSource(t *testing.T) {
	for _, block := range []string{
		`<a href="/help">Help</a>`, `<button>Open</button>`,
		`<div><img src="/logo.png"/></div>`, `<picture><img srcset="/logo.png 1x"></picture>`,
		`<svg><path d="M0 0L10 10"/></svg>`,
		`<svg><circle r="3"></circle></svg>`,
		`<table><tbody><tr><td>Shared</td></tr></tbody></table>`,
		`<style>/* caption */ body {color:red}</style>`,
		`<script>/* caption */ const text = "// not a comment";</script>`,
		`<pre>  meaningful code\n</pre>`,
	} {
		t.Run(block, func(t *testing.T) {
			prefix := "<!DOCTYPE HTML>\n<!-- keep -->\n"
			suffix := "\n<!-- tail -->"
			pages, err := DetectAutomaticTemplates([]DetectionPage{
				{Key: "/one", HTML: prefix + block + suffix},
				{Key: "/two", HTML: prefix + block + suffix},
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, page := range pages {
				if templateCount(page.HTML) != 1 || !strings.HasPrefix(page.HTML, prefix) || !strings.HasSuffix(page.HTML, suffix) {
					t.Fatalf("content not detected or source changed: %s", page.HTML)
				}
				blocks := ExtractBlocks(page.HTML)
				if len(blocks) != 1 {
					t.Fatalf("marked source cannot be extracted: %s", page.HTML)
				}
			}
			repeatedPages, err := DetectAutomaticTemplates(pages)
			if err != nil {
				t.Fatal(err)
			}
			for pageIndex := range pages {
				if repeatedPages[pageIndex].HTML != pages[pageIndex].HTML {
					t.Fatal("second detection changed existing templates")
				}
			}
		})
	}
}

func TestAutomaticTemplatesRequireDifferentPageKeys(t *testing.T) {
	block := `<div>Shared</div>`
	for _, pages := range [][]DetectionPage{
		{{Key: "/one", HTML: block + block}},
		{{Key: "/one", HTML: block}, {Key: "/one", HTML: block}},
	} {
		detectedPages, err := DetectAutomaticTemplates(pages)
		if err != nil {
			t.Fatal(err)
		}
		for _, page := range detectedPages {
			if templateCount(page.HTML) != 0 {
				t.Fatalf("one page was counted as multiple pages: %s", page.HTML)
			}
		}
	}
}

func TestResolveTemplateChangesSelectsEditedCopy(t *testing.T) {
	before := `<div class="SiteBrush-Template shared">Old</div>`
	after := `<div class="SiteBrush-Template shared">New</div>`
	for _, savedHTML := range []string{after + before, before + after, after + after} {
		resolved, err := ResolveTemplateChanges(before+before, savedHTML)
		if err != nil {
			t.Fatal(err)
		}
		updatedHTML, changed := ReplaceBlocks(savedHTML, resolved)
		if !changed || updatedHTML != after+after {
			t.Fatalf("wrong copy selected: %s", updatedHTML)
		}
	}
}

func TestResolveTemplateChangesRejectsConflictingCopies(t *testing.T) {
	before := `<div class="SiteBrush-Template shared">Old</div>`
	first := `<div class="SiteBrush-Template shared">First</div>`
	second := `<div class="SiteBrush-Template shared">Second</div>`
	for _, previousHTML := range []string{before + before, ""} {
		if _, err := ResolveTemplateChanges(previousHTML, first+second); err == nil || !strings.Contains(err.Error(), "shared") {
			t.Fatalf("conflicting copies were accepted: %v", err)
		}
	}
}

func TestResolveTemplateChangesNormalizesFormattingButPreservesContent(t *testing.T) {
	first := `<DIV class="SiteBrush-Template shared" title="keep"><span>New</span></DIV>`
	second := "<div title='keep' class='SiteBrush-Template shared'>\n<span>New</span>\n</div>"
	if _, err := ResolveTemplateChanges("", first+second); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveTemplateChanges("", first+strings.Replace(second, "New", "new", 1)); err == nil {
		t.Fatal("different text was normalized away")
	}
}
