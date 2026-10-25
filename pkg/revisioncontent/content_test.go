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
