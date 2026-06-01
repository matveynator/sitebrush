package sitebrushtemplate

import (
	"strings"
	"testing"
)

func TestSynchronizeClassesCopiesTemplateClassToSameStyleContent(t *testing.T) {
	previousHTML := `<html><head><style type="text/css">body { color: red; }</style></head></html>`
	savedHTML := `<html><head><style type="text/css" class="SiteBrush-Template mainstyle">body{color:red;}</style></head></html>`
	targetHTML := `<html><head><style
  type="text/css">
body {
	color: red;
}
</style></head></html>`

	updatedHTML, changed := SynchronizeClasses(targetHTML, ClassActionSetFromHTML(previousHTML, savedHTML))
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if !strings.Contains(updatedHTML, `class="SiteBrush-Template mainstyle"`) {
		t.Fatalf("style template class was not synchronized: %s", updatedHTML)
	}
}

func TestSynchronizeClassesMatchesStyleContentByCSSMeaning(t *testing.T) {
	previousHTML := `<html><head><style type="text/css">
/* Webfont: BloggerSans */
@font-face {
	font-family: 'BloggerSans';
	src: url('/p/font.eot'); /* IE9 */
	src: url("/p/font.eot") format('embedded-opentype'),
		url('/p/font.woff') format('woff');
	font-style: normal;
	font-weight: normal;
}
</style></head></html>`
	savedHTML := `<html><head><style type="text/css" class="SiteBrush-Template mainstyle">@font-face{font-family:'BloggerSans';src:url(/p/font.eot);src:url(/p/font.eot) format('embedded-opentype'),url(/p/font.woff) format('woff');font-style:normal;font-weight:normal}</style></head></html>`
	targetHTML := `<html><head><style
  type="text/css">
/* another comment */
@font-face {
      font-family: 'BloggerSans';
      src: url('/p/font.eot'); /* IE9 Compat Modes */
      src: url('/p/font.eot') format('embedded-opentype'),
           url('/p/font.woff') format('woff');
      font-style: normal;
      font-weight: normal;
  }
</style></head></html>`

	updatedHTML, changed := SynchronizeClasses(targetHTML, ClassActionSetFromHTML(previousHTML, savedHTML))
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if !strings.Contains(updatedHTML, `class="SiteBrush-Template mainstyle"`) {
		t.Fatalf("style template class was not synchronized: %s", updatedHTML)
	}
}

func TestSynchronizeClassesUsesPreviousElementContentWhenTemplateContentChanges(t *testing.T) {
	previousHTML := `<html><head><style type="text/css">body { color: red; }</style></head></html>`
	savedHTML := `<html><head><style type="text/css" class="SiteBrush-Template mainstyle">body { color: blue; }</style></head></html>`
	targetHTML := `<html><head><style
  type="text/css">
body {
	color: red;
}
</style></head></html>`

	updatedHTML, changed := SynchronizeClasses(targetHTML, ClassActionSetFromHTML(previousHTML, savedHTML))
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if !strings.Contains(updatedHTML, `class="SiteBrush-Template mainstyle"`) {
		t.Fatalf("style template class was not synchronized from previous content: %s", updatedHTML)
	}
}

func TestSynchronizeClassesDoesNotMatchDifferentStyleSelectors(t *testing.T) {
	previousHTML := `<html><head><style type="text/css">.menu.item{color:red}</style></head></html>`
	savedHTML := `<html><head><style type="text/css" class="SiteBrush-Template mainstyle">.menu.item{color:red}</style></head></html>`
	targetHTML := `<html><head><style type="text/css">.menu .item { color: red; }</style></head></html>`

	updatedHTML, changed := SynchronizeClasses(targetHTML, ClassActionSetFromHTML(previousHTML, savedHTML))
	if changed {
		t.Fatalf("changed = true, want false; updated html = %s", updatedHTML)
	}
}

func TestReplaceBlocksDoesNotCrossTagTypesWithSameTemplateClass(t *testing.T) {
	sourceHTML := `<html><body><div class="SiteBrush-Template shared">new</div></body></html>`
	targetHTML := `<html><body><table class="SiteBrush-Template shared"><tr><td>old</td></tr></table></body></html>`

	updatedHTML, changed := ReplaceBlocks(targetHTML, ExtractBlocks(sourceHTML))
	if changed {
		t.Fatalf("changed = true, want false; updated html = %s", updatedHTML)
	}
	if updatedHTML != targetHTML {
		t.Fatalf("updated html = %q, want %q", updatedHTML, targetHTML)
	}
}
