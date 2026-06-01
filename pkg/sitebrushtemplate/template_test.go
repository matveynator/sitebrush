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
