package revisioncontent

import "testing"

func TestHTMLHashIgnoresDocumentFormatting(t *testing.T) {
	first := `<!doctype html><html><head><title>Site</title></head><body><main><h1 class="title" id="heading">Hello world</h1><p>Text</p></main></body></html>`
	formatted := `<!DOCTYPE html>
<html>
  <head><title>Site</title></head>
  <body>
    <main>
      <h1 id='heading' class='title'>Hello   world</h1>
      <p>Text</p>
    </main>
  </body>
</html>`
	if HTMLHash(first) != HTMLHash(formatted) {
		t.Fatal("markup formatting changed the revision hash")
	}
}

func TestHTMLHashPreservesMeaningfulWhitespaceAndCode(t *testing.T) {
	base := `<div><span>Hello</span> <span>world</span></div>`
	for _, changed := range []string{
		`<div><span>Hello</span><span>world</span></div>`,
		`<div><span>Hello</span> <span>other</span></div>`,
	} {
		if HTMLHash(base) == HTMLHash(changed) {
			t.Fatalf("visible change had the same revision hash: %s", changed)
		}
	}
	if HTMLHash(`<pre>a  b</pre>`) == HTMLHash(`<pre>a b</pre>`) {
		t.Fatal("preformatted whitespace was ignored")
	}
	if HTMLHash(`<script>const x = 1;</script>`) == HTMLHash(`<script>const x=1;</script>`) {
		t.Fatal("script source was ignored")
	}
}

func TestHTMLHashPreservesStylesheetControlledWhitespace(t *testing.T) {
	testCases := []struct {
		name   string
		prefix string
		first  string
		second string
	}{
		{name: "embedded pre-wrap", prefix: `<style>.poem{white-space:pre-wrap}</style>`, first: `<div class="poem">a  b</div>`, second: `<div class="poem">a b</div>`},
		{name: "linked stylesheet", prefix: `<link rel="alternate STYLESHEET" href="/theme.css">`, first: `<div class="poem">a  b</div>`, second: `<div class="poem">a b</div>`},
		{name: "styled block layout", prefix: `<style>div{display:inline}</style>`, first: `<div>a</div> <div>b</div>`, second: `<div>a</div><div>b</div>`},
		{name: "inline display layout", first: `<div style="display:inline">a</div> <div>b</div>`, second: `<div style="display:inline">a</div><div>b</div>`},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if HTMLHash(testCase.prefix+testCase.first) == HTMLHash(testCase.prefix+testCase.second) {
				t.Fatal("stylesheet-controlled whitespace was omitted from revision identity")
			}
		})
	}
}

func TestHTMLHashIncludesDoctypeIdentifiers(t *testing.T) {
	page := `<html><head><title>Document</title></head><body><p>Text</p></body></html>`
	modern := `<!DOCTYPE html>` + page
	legacy := `<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN">` + page
	legacyWithSystem := `<!DOCTYPE html PUBLIC "-//W3C//DTD HTML 4.01 Transitional//EN" "http://www.w3.org/TR/html4/loose.dtd">` + page
	if HTMLHash(modern) == HTMLHash(legacy) {
		t.Fatal("public doctype identifier was omitted from revision identity")
	}
	if HTMLHash(legacy) == HTMLHash(legacyWithSystem) {
		t.Fatal("system doctype identifier was omitted from revision identity")
	}
}
