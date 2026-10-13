package sitebrushtemplate

import (
	"fmt"
	"sort"
	"strings"
)

// ResolveTemplateChanges chooses the edited version instead of whichever copy
// happened to occur last. Conflicts must be checked before any save side effect.
func ResolveTemplateChanges(previousHTML, savedHTML string) (map[string]string, error) {
	previousVersions := make(map[string]map[string]string)
	for _, previousMatch := range scanMatches(previousHTML) {
		addTemplateVersion(previousVersions, previousMatch)
	}
	savedVersions := make(map[string]map[string]string)
	for _, savedMatch := range scanMatches(savedHTML) {
		addTemplateVersion(savedVersions, savedMatch)
	}
	identifiers := make([]string, 0, len(savedVersions))
	for identifier := range savedVersions {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	resolvedBlocks := make(map[string]string, len(identifiers))
	for _, identifier := range identifiers {
		versions := savedVersions[identifier]
		if len(versions) == 1 {
			for _, block := range versions {
				resolvedBlocks[identifier] = block
			}
			continue
		}
		changedVersions := 0
		for canonical, block := range versions {
			if _, unchanged := previousVersions[identifier][canonical]; !unchanged {
				changedVersions++
				resolvedBlocks[identifier] = block
			}
		}
		if changedVersions != 1 {
			_, templateName, _ := strings.Cut(identifier, "\x00")
			return nil, fmt.Errorf("conflicting copies of SiteBrush-Template %q: use the same content for every edited copy", templateName)
		}
	}
	return resolvedBlocks, nil
}

func addTemplateVersion(versionsByID map[string]map[string]string, currentMatch match) {
	versions := versionsByID[currentMatch.id]
	if versions == nil {
		versions = make(map[string]string)
		versionsByID[currentMatch.id] = versions
	}
	canonical := currentMatch.block
	root, _, err := parseDetectionSource(currentMatch.block)
	if err == nil {
		var canonicalSource strings.Builder
		for child := root.FirstChild; child != nil; child = child.NextSibling {
			writeCanonicalDetectionNode(&canonicalSource, child)
		}
		canonical = canonicalSource.String()
	}
	// Keep the first occurrence so formatting choices remain deterministic.
	if _, exists := versions[canonical]; !exists {
		versions[canonical] = currentMatch.block
	}
}
