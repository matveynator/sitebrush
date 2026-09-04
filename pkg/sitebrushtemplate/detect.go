package sitebrushtemplate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

const (
	AutomaticTemplateMinimumElementCount   = 10
	AutomaticTemplateMinimumCanonicalBytes = 160
)

// DetectionPage keeps the page identity beside its HTML while a complete import is analyzed.
type DetectionPage struct {
	Key  string
	HTML string
}

type detectionDocument struct {
	page DetectionPage
	root *html.Node
}

type subtreeFingerprint struct {
	hash           [sha256.Size]byte
	elementCount   int
	canonicalBytes int
	empty          bool
}

type templateCandidate struct {
	pageKey     string
	node        *html.Node
	fingerprint subtreeFingerprint
}

type templateCandidateGroup struct {
	canonical    string
	hash         [sha256.Size]byte
	elementCount int
	occurrences  []*templateCandidate
}

// DetectAutomaticTemplates adds ordinary SiteBrush-Template classes to useful
// repeated subtrees. The returned pages remain in the same order as the input.
func DetectAutomaticTemplates(pageList []DetectionPage) ([]DetectionPage, error) {
	documentList := make([]detectionDocument, 0, len(pageList))
	candidateByHash := make(map[[sha256.Size]byte][]*templateCandidate)
	existingTemplateNodes := make(map[*html.Node]struct{})
	usedTemplateIdentifiers := make(map[string]struct{})

	for _, page := range pageList {
		root, err := html.Parse(strings.NewReader(page.HTML))
		if err != nil {
			return nil, fmt.Errorf("parse page %s for SiteBrush-Template detection: %w", page.Key, err)
		}
		documentList = append(documentList, detectionDocument{page: page, root: root})
		collectExistingTemplateMarkup(root, existingTemplateNodes, usedTemplateIdentifiers)
		fingerprintDOMSubtrees(root, page.Key, candidateByHash)
	}

	groupList := matchingTemplateCandidateGroups(candidateByHash)
	sort.Slice(groupList, func(leftIndex, rightIndex int) bool {
		leftGroup := groupList[leftIndex]
		rightGroup := groupList[rightIndex]
		if leftGroup.elementCount != rightGroup.elementCount {
			return leftGroup.elementCount > rightGroup.elementCount
		}
		return leftGroup.canonical < rightGroup.canonical
	})

	selectedNodes := make(map[*html.Node]struct{}, len(existingTemplateNodes))
	for node := range existingTemplateNodes {
		selectedNodes[node] = struct{}{}
	}
	changedNodes := make(map[*html.Node]struct{})
	for _, group := range groupList {
		if templateGroupIsFullyNested(group, selectedNodes) {
			continue
		}
		identifierClass := automaticTemplateIdentifier(group, usedTemplateIdentifiers)
		identifier := strings.TrimPrefix(identifierClass, "sitebrush-template-")
		usedTemplateIdentifiers[strings.ToLower(identifier)] = struct{}{}
		for _, occurrence := range group.occurrences {
			addAutomaticTemplateClasses(occurrence.node, identifierClass)
			selectedNodes[occurrence.node] = struct{}{}
			changedNodes[occurrence.node] = struct{}{}
		}
	}

	result := make([]DetectionPage, 0, len(documentList))
	for _, document := range documentList {
		if !documentContainsChangedNode(document.root, changedNodes) {
			result = append(result, document.page)
			continue
		}
		var renderedHTML bytes.Buffer
		if err := html.Render(&renderedHTML, document.root); err != nil {
			return nil, fmt.Errorf("render page %s after SiteBrush-Template detection: %w", document.page.Key, err)
		}
		result = append(result, DetectionPage{Key: document.page.Key, HTML: renderedHTML.String()})
	}
	return result, nil
}

func fingerprintDOMSubtrees(root *html.Node, pageKey string, candidateByHash map[[sha256.Size]byte][]*templateCandidate) subtreeFingerprint {
	if root == nil {
		return subtreeFingerprint{}
	}
	childFingerprints := make([]subtreeFingerprint, 0)
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		childFingerprints = append(childFingerprints, fingerprintDOMSubtrees(child, pageKey, candidateByHash))
	}
	fingerprint := canonicalNodeFingerprint(root, childFingerprints)
	if automaticTemplateCandidateAllowed(root, fingerprint) {
		candidate := &templateCandidate{pageKey: pageKey, node: root, fingerprint: fingerprint}
		candidateByHash[fingerprint.hash] = append(candidateByHash[fingerprint.hash], candidate)
	}
	return fingerprint
}

func canonicalNodeFingerprint(node *html.Node, childFingerprints []subtreeFingerprint) subtreeFingerprint {
	if node == nil {
		return subtreeFingerprint{empty: true}
	}
	var hashSource bytes.Buffer
	elementCount := 0
	canonicalBytes := 0
	switch node.Type {
	case html.ElementNode:
		elementCount = 1
		tagName := strconv.Quote(strings.ToLower(node.Data))
		hashSource.WriteByte('<')
		hashSource.WriteString(tagName)
		canonicalBytes = 1 + len(tagName) + 1
		for _, attribute := range canonicalDetectionAttributes(node.Attr) {
			hashSource.WriteByte('a')
			hashSource.WriteString(attribute)
			canonicalBytes += 1 + len(attribute)
		}
		hashSource.WriteByte('>')
		if strings.EqualFold(node.Data, "style") {
			styleText := strconv.Quote(normalizedCSS(elementText(node)))
			hashSource.WriteByte('s')
			hashSource.WriteString(styleText)
			canonicalBytes += 1 + len(styleText)
		} else if strings.EqualFold(node.Data, "script") || strings.EqualFold(node.Data, "pre") || strings.EqualFold(node.Data, "textarea") {
			rawText := strconv.Quote(elementText(node))
			hashSource.WriteByte('r')
			hashSource.WriteString(rawText)
			canonicalBytes += 1 + len(rawText)
		} else {
			for _, childFingerprint := range childFingerprints {
				if childFingerprint.empty {
					continue
				}
				hashSource.Write(childFingerprint.hash[:])
				canonicalBytes += childFingerprint.canonicalBytes
			}
		}
		hashSource.WriteString("</")
		hashSource.WriteString(tagName)
		hashSource.WriteByte('>')
		canonicalBytes += 2 + len(tagName) + 1
	case html.TextNode:
		normalizedText := normalizedDetectionText(node.Data)
		if normalizedText == "" {
			return subtreeFingerprint{empty: true}
		}
		quotedText := strconv.Quote(normalizedText)
		hashSource.WriteByte('t')
		hashSource.WriteString(quotedText)
		canonicalBytes = 1 + len(quotedText)
	default:
		return subtreeFingerprint{empty: true}
	}
	for _, childFingerprint := range childFingerprints {
		elementCount += childFingerprint.elementCount
	}
	return subtreeFingerprint{hash: sha256.Sum256(hashSource.Bytes()), elementCount: elementCount, canonicalBytes: canonicalBytes}
}

func canonicalDetectionNode(node *html.Node) string {
	var canonical strings.Builder
	writeCanonicalDetectionNode(&canonical, node)
	return canonical.String()
}

func writeCanonicalDetectionNode(canonical *strings.Builder, node *html.Node) {
	if node == nil {
		return
	}
	switch node.Type {
	case html.ElementNode:
		tagName := strconv.Quote(strings.ToLower(node.Data))
		canonical.WriteByte('<')
		canonical.WriteString(tagName)
		for _, attribute := range canonicalDetectionAttributes(node.Attr) {
			canonical.WriteByte('a')
			canonical.WriteString(attribute)
		}
		canonical.WriteByte('>')
		if strings.EqualFold(node.Data, "style") {
			canonical.WriteByte('s')
			canonical.WriteString(strconv.Quote(normalizedCSS(elementText(node))))
		} else if strings.EqualFold(node.Data, "script") || strings.EqualFold(node.Data, "pre") || strings.EqualFold(node.Data, "textarea") {
			canonical.WriteByte('r')
			canonical.WriteString(strconv.Quote(elementText(node)))
		} else {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				writeCanonicalDetectionNode(canonical, child)
			}
		}
		canonical.WriteString("</")
		canonical.WriteString(tagName)
		canonical.WriteByte('>')
	case html.TextNode:
		normalizedText := normalizedDetectionText(node.Data)
		if normalizedText != "" {
			canonical.WriteByte('t')
			canonical.WriteString(strconv.Quote(normalizedText))
		}
	}
}

func canonicalDetectionAttributes(attributeList []html.Attribute) []string {
	canonicalAttributes := make([]string, 0, len(attributeList))
	for _, attribute := range attributeList {
		attributeName := strings.ToLower(strings.TrimSpace(attribute.Key))
		if attribute.Namespace != "" {
			attributeName = strings.ToLower(attribute.Namespace) + ":" + attributeName
		}
		if attributeName == "" {
			continue
		}
		attributeValue := attribute.Val
		if attributeName == "class" {
			attributeValue = strings.Join(strings.Fields(attributeValue), " ")
		}
		canonicalAttributes = append(canonicalAttributes, strconv.Quote(attributeName)+strconv.Quote(attributeValue))
	}
	sort.Strings(canonicalAttributes)
	return canonicalAttributes
}

func normalizedDetectionText(sourceText string) string {
	return strings.Join(strings.Fields(sourceText), " ")
}

func elementText(node *html.Node) string {
	var text strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			text.WriteString(child.Data)
		}
	}
	return text.String()
}

func automaticTemplateCandidateAllowed(node *html.Node, fingerprint subtreeFingerprint) bool {
	if node == nil || node.Type != html.ElementNode || nodeHasSiteBrushTemplate(node) {
		return false
	}
	switch strings.ToLower(node.Data) {
	case "html", "head", "body", "script", "link", "meta", "title":
		return false
	case "style":
		return fingerprint.canonicalBytes >= AutomaticTemplateMinimumCanonicalBytes
	}
	return fingerprint.elementCount >= AutomaticTemplateMinimumElementCount && fingerprint.canonicalBytes >= AutomaticTemplateMinimumCanonicalBytes
}

func matchingTemplateCandidateGroups(candidateByHash map[[sha256.Size]byte][]*templateCandidate) []*templateCandidateGroup {
	groupList := make([]*templateCandidateGroup, 0)
	for hashValue, hashCandidates := range candidateByHash {
		candidateByCanonical := make(map[string][]*templateCandidate)
		for _, candidate := range hashCandidates {
			canonical := canonicalDetectionNode(candidate.node)
			candidateByCanonical[canonical] = append(candidateByCanonical[canonical], candidate)
		}
		for canonical, canonicalCandidates := range candidateByCanonical {
			pageKeys := make(map[string]struct{})
			for _, candidate := range canonicalCandidates {
				pageKeys[candidate.pageKey] = struct{}{}
			}
			if len(pageKeys) < 2 {
				continue
			}
			groupList = append(groupList, &templateCandidateGroup{canonical: canonical, hash: hashValue, elementCount: canonicalCandidates[0].fingerprint.elementCount, occurrences: canonicalCandidates})
		}
	}
	return groupList
}

func templateGroupIsFullyNested(group *templateCandidateGroup, selectedNodes map[*html.Node]struct{}) bool {
	for _, occurrence := range group.occurrences {
		if !nodeHasSelectedAncestor(occurrence.node, selectedNodes) {
			return false
		}
	}
	return true
}

func nodeHasSelectedAncestor(node *html.Node, selectedNodes map[*html.Node]struct{}) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if _, selected := selectedNodes[parent]; selected {
			return true
		}
	}
	return false
}

func automaticTemplateIdentifier(group *templateCandidateGroup, usedIdentifiers map[string]struct{}) string {
	purpose := automaticTemplatePurpose(group.occurrences[0].node)
	hashText := hex.EncodeToString(group.hash[:])
	for hashLength := 12; hashLength <= len(hashText); hashLength += 4 {
		identifier := purpose + "-" + hashText[:hashLength]
		identifierClass := "sitebrush-template-" + identifier
		if _, used := usedIdentifiers[strings.ToLower(identifier)]; !used {
			return identifierClass
		}
	}
	for suffix := 2; ; suffix++ {
		identifier := fmt.Sprintf("%s-%s-%d", purpose, hashText, suffix)
		identifierClass := "sitebrush-template-" + identifier
		if _, used := usedIdentifiers[strings.ToLower(identifier)]; !used {
			return identifierClass
		}
	}
}

func automaticTemplatePurpose(node *html.Node) string {
	if node == nil {
		return "block"
	}
	switch strings.ToLower(node.Data) {
	case "header", "footer", "nav", "aside", "table", "style":
		return strings.ToLower(node.Data)
	}
	for _, attribute := range node.Attr {
		if !strings.EqualFold(attribute.Key, "role") {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(attribute.Val)) {
		case "banner":
			return "header"
		case "contentinfo":
			return "footer"
		case "navigation":
			return "nav"
		case "complementary":
			return "sidebar"
		}
	}
	return "block"
}

func collectExistingTemplateMarkup(root *html.Node, existingTemplateNodes map[*html.Node]struct{}, usedIdentifierClasses map[string]struct{}) {
	for node := root; node != nil; node = nextDOMNode(root, node) {
		if node.Type != html.ElementNode {
			continue
		}
		classValue := nodeClassValue(node)
		if classValue == "" {
			continue
		}
		if classListHasSiteBrushTemplate(classValue) {
			existingTemplateNodes[node] = struct{}{}
			if identifier := templateIdentifierFromClassList(classValue); identifier != "" {
				usedIdentifierClasses[strings.ToLower(identifier)] = struct{}{}
			}
		}
	}
}

func nextDOMNode(root, current *html.Node) *html.Node {
	if current.FirstChild != nil {
		return current.FirstChild
	}
	for current != nil && current != root {
		if current.NextSibling != nil {
			return current.NextSibling
		}
		current = current.Parent
	}
	return nil
}

func nodeHasSiteBrushTemplate(node *html.Node) bool {
	return classListHasSiteBrushTemplate(nodeClassValue(node))
}

func nodeClassValue(node *html.Node) string {
	if node == nil {
		return ""
	}
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, "class") {
			return attribute.Val
		}
	}
	return ""
}

func addAutomaticTemplateClasses(node *html.Node, identifierClass string) {
	for attributeIndex := range node.Attr {
		if !strings.EqualFold(node.Attr[attributeIndex].Key, "class") {
			continue
		}
		existingClasses := strings.Fields(node.Attr[attributeIndex].Val)
		node.Attr[attributeIndex].Val = strings.Join(append([]string{"SiteBrush-Template", identifierClass}, existingClasses...), " ")
		return
	}
	node.Attr = append(node.Attr, html.Attribute{Key: "class", Val: "SiteBrush-Template " + identifierClass})
}

func documentContainsChangedNode(root *html.Node, changedNodes map[*html.Node]struct{}) bool {
	for node := root; node != nil; node = nextDOMNode(root, node) {
		if _, changed := changedNodes[node]; changed {
			return true
		}
	}
	return false
}

func validCSSClassName(className string) bool {
	for characterIndex, classRune := range className {
		if unicode.IsLetter(classRune) || classRune == '_' || classRune == '-' || (characterIndex > 0 && unicode.IsDigit(classRune)) {
			continue
		}
		return false
	}
	return className != ""
}
