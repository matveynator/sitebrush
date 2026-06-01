package sitebrushtemplate

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

type match struct {
	start int
	end   int
	id    string
	block string
}

type ClassActionSet struct {
	addByKey        map[string]classRewrite
	addByContentKey map[string]classRewrite
	removeByKey     map[string]struct{}
}

type classRewrite struct {
	classNames []string
}

type classElement struct {
	startTagStart int
	startTagEnd   int
	tagName       string
	classKey      string
	contentKey    string
	matchKey      string
	hasTemplate   bool
	classNames    []string
}

type classOpenElement struct {
	tagName       string
	startTagStart int
	startTagEnd   int
	classKey      string
	hasTemplate   bool
	classNames    []string
}

type classEdit struct {
	startTagStart int
	startTagEnd   int
	rewrite       classRewrite
	remove        bool
}

type openElement struct {
	tagName    string
	start      int
	templateID string
}

var htmlClassAttributePattern = regexp.MustCompile(`(?is)\sclass\s*=\s*("([^"]*)"|'([^']*)'|([^\s"'=<>]+))`)
var cssSingleQuotedURLPattern = regexp.MustCompile(`(?i)url\('([^']*)'\)`)
var cssDoubleQuotedURLPattern = regexp.MustCompile(`(?i)url\("([^"]*)"\)`)
var cssSemicolonBeforeBracePattern = regexp.MustCompile(`;+\}`)

func ExtractBlocks(sourceHTML string) map[string]string {
	matchList := scanMatches(sourceHTML)
	if len(matchList) == 0 {
		return nil
	}
	blockByID := make(map[string]string, len(matchList))
	for _, currentMatch := range matchList {
		blockByID[currentMatch.id] = currentMatch.block
	}
	return blockByID
}

func ReplaceBlocks(pageHTML string, blockByID map[string]string) (string, bool) {
	matchList := scanMatches(pageHTML)
	if len(matchList) == 0 {
		return pageHTML, false
	}
	matchList = sortedNonOverlappingMatches(matchList)
	if len(matchList) == 0 {
		return pageHTML, false
	}

	var updatedHTML strings.Builder
	updatedHTML.Grow(len(pageHTML))
	changed := false
	previousEnd := 0
	for _, currentMatch := range matchList {
		replacementBlock, found := blockByID[currentMatch.id]
		if !found {
			continue
		}
		updatedHTML.WriteString(pageHTML[previousEnd:currentMatch.start])
		updatedHTML.WriteString(replacementBlock)
		previousEnd = currentMatch.end
		changed = true
	}
	if !changed {
		return pageHTML, false
	}
	updatedHTML.WriteString(pageHTML[previousEnd:])
	return updatedHTML.String(), true
}

func ClassActionSetFromHTML(previousHTML, savedHTML string) ClassActionSet {
	previousTemplateKeys, previousPlainKeys := classKeySets(previousHTML)
	savedTemplateKeys, savedPlainKeys := classKeySets(savedHTML)
	previousElements := scanClassElements(previousHTML)
	savedElements := scanClassElements(savedHTML)
	previousElementBySavedIndex := pairElementsByTagOrder(previousElements, savedElements)

	actionSet := ClassActionSet{
		addByKey:        make(map[string]classRewrite),
		addByContentKey: make(map[string]classRewrite),
		removeByKey:     make(map[string]struct{}),
	}
	for savedElementIndex, savedElement := range savedElements {
		if !savedElement.hasTemplate {
			continue
		}
		_, wasPlain := previousPlainKeys[savedElement.matchKey]
		_, wasTemplate := previousTemplateKeys[savedElement.matchKey]
		if wasPlain || !wasTemplate {
			actionSet.addByKey[savedElement.matchKey] = classRewrite{classNames: savedElement.classNames}
			if savedElement.contentKey != "" {
				actionSet.addByContentKey[classContentElementKey(savedElement.tagName, savedElement.contentKey)] = classRewrite{classNames: savedElement.classNames}
			}
		}
		previousPeer, hasPreviousPeer := previousElementBySavedIndex[savedElementIndex]
		if hasPreviousPeer && !previousPeer.hasTemplate {
			actionSet.addByKey[previousPeer.matchKey] = classRewrite{classNames: savedElement.classNames}
			if previousPeer.contentKey != "" {
				actionSet.addByContentKey[classContentElementKey(previousPeer.tagName, previousPeer.contentKey)] = classRewrite{classNames: savedElement.classNames}
			}
		}
		for _, previousElement := range previousElements {
			if previousElement.hasTemplate || previousElement.tagName != savedElement.tagName || previousElement.contentKey != savedElement.contentKey {
				continue
			}
			actionSet.addByKey[previousElement.matchKey] = classRewrite{classNames: savedElement.classNames}
			if savedElement.contentKey != "" {
				actionSet.addByContentKey[classContentElementKey(savedElement.tagName, savedElement.contentKey)] = classRewrite{classNames: savedElement.classNames}
			}
		}
	}
	for innerKey := range savedTemplateKeys {
		_, wasPlain := previousPlainKeys[innerKey]
		_, wasTemplate := previousTemplateKeys[innerKey]
		if wasPlain || !wasTemplate {
			if rewrite, found := actionSet.addByKey[innerKey]; !found || len(rewrite.classNames) == 0 {
				actionSet.addByKey[innerKey] = classRewrite{classNames: templateClassNamesForKey(innerKey, savedElements)}
			}
		}
	}
	for innerKey := range previousTemplateKeys {
		if _, isPlain := savedPlainKeys[innerKey]; isPlain {
			actionSet.removeByKey[innerKey] = struct{}{}
			delete(actionSet.addByKey, innerKey)
		}
	}
	return actionSet
}

func pairElementsByTagOrder(previousElements, savedElements []classElement) map[int]classElement {
	previousElementsByTag := make(map[string][]classElement)
	for _, previousElement := range previousElements {
		previousElementsByTag[previousElement.tagName] = append(previousElementsByTag[previousElement.tagName], previousElement)
	}
	savedTagCount := make(map[string]int)
	previousElementBySavedIndex := make(map[int]classElement)
	for savedElementIndex, savedElement := range savedElements {
		tagIndex := savedTagCount[savedElement.tagName]
		savedTagCount[savedElement.tagName] = tagIndex + 1
		previousTagElements := previousElementsByTag[savedElement.tagName]
		if tagIndex >= len(previousTagElements) {
			continue
		}
		previousElementBySavedIndex[savedElementIndex] = previousTagElements[tagIndex]
	}
	return previousElementBySavedIndex
}

func (actionSet ClassActionSet) IsEmpty() bool {
	return len(actionSet.addByKey) == 0 && len(actionSet.addByContentKey) == 0 && len(actionSet.removeByKey) == 0
}

func SynchronizeClasses(sourceHTML string, actionSet ClassActionSet) (string, bool) {
	if actionSet.IsEmpty() {
		return sourceHTML, false
	}
	editList := make([]classEdit, 0)
	for _, element := range scanClassElements(sourceHTML) {
		if _, removeTemplate := actionSet.removeByKey[element.matchKey]; removeTemplate && element.hasTemplate {
			editList = append(editList, classEdit{startTagStart: element.startTagStart, startTagEnd: element.startTagEnd, remove: true})
			continue
		}
		if rewrite, addTemplate := actionSet.addByKey[element.matchKey]; addTemplate {
			editList = append(editList, classEdit{startTagStart: element.startTagStart, startTagEnd: element.startTagEnd, rewrite: rewrite})
			continue
		}
		if element.contentKey == "" {
			continue
		}
		if rewrite, addTemplate := actionSet.addByContentKey[classContentElementKey(element.tagName, element.contentKey)]; addTemplate {
			editList = append(editList, classEdit{startTagStart: element.startTagStart, startTagEnd: element.startTagEnd, rewrite: rewrite})
		}
	}
	if len(editList) == 0 {
		return sourceHTML, false
	}
	sort.Slice(editList, func(leftIndex, rightIndex int) bool {
		return editList[leftIndex].startTagStart < editList[rightIndex].startTagStart
	})

	var updatedHTML strings.Builder
	updatedHTML.Grow(len(sourceHTML) + len(editList)*len("SiteBrush-Template "))
	previousEnd := 0
	changed := false
	for _, edit := range editList {
		if edit.startTagStart < previousEnd || edit.startTagEnd > len(sourceHTML) {
			continue
		}
		startTag := sourceHTML[edit.startTagStart:edit.startTagEnd]
		updatedStartTag := rewriteClassStartTag(startTag, edit.rewrite, edit.remove)
		updatedHTML.WriteString(sourceHTML[previousEnd:edit.startTagStart])
		updatedHTML.WriteString(updatedStartTag)
		previousEnd = edit.startTagEnd
		if updatedStartTag != startTag {
			changed = true
		}
	}
	if !changed {
		return sourceHTML, false
	}
	updatedHTML.WriteString(sourceHTML[previousEnd:])
	return updatedHTML.String(), true
}

func templateClassNamesForKey(matchKey string, elements []classElement) []string {
	for _, element := range elements {
		if element.matchKey == matchKey && element.hasTemplate {
			return element.classNames
		}
	}
	return []string{"SiteBrush-Template"}
}

func classKeySets(sourceHTML string) (map[string]struct{}, map[string]struct{}) {
	templateKeys := make(map[string]struct{})
	plainKeys := make(map[string]struct{})
	for _, element := range scanClassElements(sourceHTML) {
		if element.hasTemplate {
			templateKeys[element.matchKey] = struct{}{}
			continue
		}
		plainKeys[element.matchKey] = struct{}{}
	}
	return templateKeys, plainKeys
}

func scanClassElements(sourceHTML string) []classElement {
	tokenizer := html.NewTokenizer(strings.NewReader(sourceHTML))
	openStack := make([]classOpenElement, 0)
	elementList := make([]classElement, 0)
	offset := 0
	for {
		tokenType := tokenizer.Next()
		rawToken := tokenizer.Raw()
		tokenStart := offset
		tokenEnd := tokenStart + len(rawToken)
		offset = tokenEnd
		switch tokenType {
		case html.ErrorToken:
			return elementList
		case html.StartTagToken:
			token := tokenizer.Token()
			tagName := strings.ToLower(token.Data)
			classKey := normalizedClassKey(token)
			classNames := normalizedClassNames(token)
			if isHTMLVoidElement(tagName) {
				elementList = append(elementList, classElement{
					startTagStart: tokenStart,
					startTagEnd:   tokenEnd,
					tagName:       tagName,
					classKey:      classKey,
					contentKey:    "",
					matchKey:      classElementKey(tagName, classKey, ""),
					hasTemplate:   tokenHasSiteBrushTemplateClass(token),
					classNames:    classNames,
				})
				continue
			}
			openStack = append(openStack, classOpenElement{
				tagName:       tagName,
				startTagStart: tokenStart,
				startTagEnd:   tokenEnd,
				classKey:      classKey,
				hasTemplate:   tokenHasSiteBrushTemplateClass(token),
				classNames:    classNames,
			})
		case html.SelfClosingTagToken:
			token := tokenizer.Token()
			tagName := strings.ToLower(token.Data)
			classKey := normalizedClassKey(token)
			elementList = append(elementList, classElement{
				startTagStart: tokenStart,
				startTagEnd:   tokenEnd,
				tagName:       tagName,
				classKey:      classKey,
				contentKey:    "",
				matchKey:      classElementKey(tagName, classKey, ""),
				hasTemplate:   tokenHasSiteBrushTemplateClass(token),
				classNames:    normalizedClassNames(token),
			})
		case html.EndTagToken:
			token := tokenizer.Token()
			for len(openStack) > 0 {
				lastIndex := len(openStack) - 1
				openElement := openStack[lastIndex]
				openStack = openStack[:lastIndex]
				if !strings.EqualFold(openElement.tagName, token.Data) {
					continue
				}
				contentKey := normalizedElementContent(openElement.tagName, sourceHTML[openElement.startTagEnd:tokenStart])
				elementList = append(elementList, classElement{
					startTagStart: openElement.startTagStart,
					startTagEnd:   openElement.startTagEnd,
					tagName:       openElement.tagName,
					classKey:      openElement.classKey,
					contentKey:    contentKey,
					matchKey:      classElementKey(openElement.tagName, openElement.classKey, contentKey),
					hasTemplate:   openElement.hasTemplate,
					classNames:    openElement.classNames,
				})
				break
			}
		}
	}
}

func classElementKey(tagName, classKey, contentKey string) string {
	return strings.ToLower(tagName) + "\x00" + classKey + "\x00" + contentKey
}

func classContentElementKey(tagName, contentKey string) string {
	return strings.ToLower(tagName) + "\x00" + contentKey
}

func tokenHasSiteBrushTemplateClass(token html.Token) bool {
	for _, attribute := range token.Attr {
		if strings.EqualFold(attribute.Key, "class") && classListHasSiteBrushTemplate(attribute.Val) {
			return true
		}
	}
	return false
}

func normalizedClassKey(token html.Token) string {
	classNameList := normalizedClassNames(token)
	filteredList := classNameList[:0]
	for _, className := range classNameList {
		if shouldIgnoreIdentityClass(className) {
			continue
		}
		filteredList = append(filteredList, className)
	}
	return strings.Join(filteredList, " ")
}

func normalizedClassNames(token html.Token) []string {
	classNameSet := make(map[string]struct{})
	for _, attribute := range token.Attr {
		if !strings.EqualFold(attribute.Key, "class") {
			continue
		}
		for _, className := range strings.Fields(attribute.Val) {
			classNameSet[className] = struct{}{}
		}
	}
	classNameList := make([]string, 0, len(classNameSet))
	for className := range classNameSet {
		classNameList = append(classNameList, className)
	}
	sort.Strings(classNameList)
	if classListHasSiteBrushTemplate(strings.Join(classNameList, " ")) {
		classNameList = prependSiteBrushTemplateClass(strings.Join(classNameList, " "))
	}
	return classNameList
}

func normalizedInnerHTML(innerHTML string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(innerHTML))
	var normalizedHTML strings.Builder
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}
		token := tokenizer.Token()
		switch tokenType {
		case html.TextToken:
			normalizedHTML.WriteString(normalizedText(token.Data))
		case html.StartTagToken:
			normalizedHTML.WriteString(normalizedTokenStart(token, false))
		case html.SelfClosingTagToken:
			normalizedHTML.WriteString(normalizedTokenStart(token, true))
		case html.EndTagToken:
			normalizedHTML.WriteString("</")
			normalizedHTML.WriteString(strings.ToLower(strings.TrimSpace(token.Data)))
			normalizedHTML.WriteByte('>')
		}
	}
	return normalizedHTML.String()
}

func normalizedElementContent(tagName, innerHTML string) string {
	if strings.EqualFold(tagName, "style") {
		return normalizedCSS(innerHTML)
	}
	return normalizedInnerHTML(innerHTML)
}

func normalizedCSS(cssText string) string {
	cssWithoutComments := stripCSSComments(cssText)
	var normalizedCSS strings.Builder
	normalizedCSS.Grow(len(cssWithoutComments))
	quoteRune := rune(0)
	escaped := false
	pendingWhitespace := false
	previousRune := rune(0)
	for _, currentRune := range cssWithoutComments {
		if quoteRune != 0 {
			normalizedCSS.WriteRune(currentRune)
			if escaped {
				escaped = false
				continue
			}
			if currentRune == '\\' {
				escaped = true
				continue
			}
			if currentRune == quoteRune {
				quoteRune = 0
			}
			previousRune = currentRune
			continue
		}
		if unicode.IsSpace(currentRune) {
			pendingWhitespace = true
			continue
		}
		if pendingWhitespace && normalizedCSS.Len() > 0 && cssWhitespaceIsSignificant(previousRune, currentRune) {
			normalizedCSS.WriteByte(' ')
		}
		pendingWhitespace = false
		if currentRune == '\'' || currentRune == '"' {
			quoteRune = currentRune
			normalizedCSS.WriteRune(currentRune)
			previousRune = currentRune
			continue
		}
		normalizedCSS.WriteRune(currentRune)
		previousRune = currentRune
	}
	normalizedText := normalizedCSS.String()
	normalizedText = cssSingleQuotedURLPattern.ReplaceAllString(normalizedText, `url($1)`)
	normalizedText = cssDoubleQuotedURLPattern.ReplaceAllString(normalizedText, `url($1)`)
	normalizedText = cssSemicolonBeforeBracePattern.ReplaceAllString(normalizedText, `}`)
	return normalizedText
}

func cssWhitespaceIsSignificant(previousRune, nextRune rune) bool {
	if previousRune == 0 || nextRune == 0 {
		return false
	}
	if strings.ContainsRune("{([:,;>/+~=", previousRune) {
		return false
	}
	if strings.ContainsRune("{})],:;/+~=", nextRune) {
		return false
	}
	return cssCanEndSeparatedToken(previousRune) && cssCanStartSeparatedToken(nextRune)
}

func cssCanEndSeparatedToken(currentRune rune) bool {
	return unicode.IsLetter(currentRune) || unicode.IsDigit(currentRune) || currentRune == '_' || currentRune == '-' || currentRune == ')' || currentRune == ']' || currentRune == '\'' || currentRune == '"'
}

func cssCanStartSeparatedToken(currentRune rune) bool {
	return unicode.IsLetter(currentRune) || unicode.IsDigit(currentRune) || currentRune == '_' || currentRune == '-' || currentRune == '.' || currentRune == '#' || currentRune == '[' || currentRune == ':' || currentRune == '*' || currentRune == '\'' || currentRune == '"'
}

func stripCSSComments(cssText string) string {
	var strippedCSS strings.Builder
	strippedCSS.Grow(len(cssText))
	quoteRune := rune(0)
	escaped := false
	for index := 0; index < len(cssText); {
		currentRune, currentSize := rune(cssText[index]), 1
		if currentRune >= utf8.RuneSelf {
			currentRune, currentSize = utf8.DecodeRuneInString(cssText[index:])
		}
		if quoteRune != 0 {
			strippedCSS.WriteRune(currentRune)
			index += currentSize
			if escaped {
				escaped = false
				continue
			}
			if currentRune == '\\' {
				escaped = true
				continue
			}
			if currentRune == quoteRune {
				quoteRune = 0
			}
			continue
		}
		if currentRune == '\'' || currentRune == '"' {
			quoteRune = currentRune
			strippedCSS.WriteRune(currentRune)
			index += currentSize
			continue
		}
		if currentRune == '/' && index+1 < len(cssText) && cssText[index+1] == '*' {
			index += 2
			for index+1 < len(cssText) {
				if cssText[index] == '*' && cssText[index+1] == '/' {
					index += 2
					break
				}
				index++
			}
			continue
		}
		strippedCSS.WriteRune(currentRune)
		index += currentSize
	}
	return strippedCSS.String()
}

func normalizedText(text string) string {
	return strings.Map(func(textRune rune) rune {
		if unicode.IsSpace(textRune) {
			return -1
		}
		return textRune
	}, text)
}

func normalizedTokenStart(token html.Token, selfClosing bool) string {
	tagName := strings.ToLower(strings.TrimSpace(token.Data))
	attributeList := normalizedAttributes(token.Attr)
	var normalizedStart strings.Builder
	normalizedStart.WriteByte('<')
	normalizedStart.WriteString(tagName)
	for _, attribute := range attributeList {
		normalizedStart.WriteByte(' ')
		normalizedStart.WriteString(attribute)
	}
	if selfClosing {
		normalizedStart.WriteByte('/')
	}
	normalizedStart.WriteByte('>')
	return normalizedStart.String()
}

func normalizedAttributes(attributes []html.Attribute) []string {
	attributeList := make([]string, 0, len(attributes))
	for _, attribute := range attributes {
		attributeName := strings.ToLower(strings.TrimSpace(attribute.Key))
		if attributeName == "" {
			continue
		}
		attributeValue := strings.TrimSpace(attribute.Val)
		if attributeName == "class" {
			attributeValue = normalizedInnerClassValue(attributeValue)
			if attributeValue == "" {
				continue
			}
		}
		attributeList = append(attributeList, attributeName+"="+attributeValue)
	}
	sort.Strings(attributeList)
	return attributeList
}

func normalizedInnerClassValue(classValue string) string {
	classNameSet := make(map[string]struct{})
	for _, className := range strings.Fields(classValue) {
		if shouldIgnoreIdentityClass(className) {
			continue
		}
		classNameSet[className] = struct{}{}
	}
	classNameList := make([]string, 0, len(classNameSet))
	for className := range classNameSet {
		classNameList = append(classNameList, className)
	}
	sort.Strings(classNameList)
	return strings.Join(classNameList, " ")
}

func shouldIgnoreIdentityClass(className string) bool {
	return strings.EqualFold(className, "SiteBrush-Template") || strings.EqualFold(className, "selected")
}

func isHTMLVoidElement(tagName string) bool {
	switch strings.ToLower(tagName) {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}

func rewriteClassStartTag(startTag string, rewrite classRewrite, remove bool) string {
	classMatch := htmlClassAttributePattern.FindStringSubmatchIndex(startTag)
	if classMatch == nil {
		if remove {
			return startTag
		}
		return insertClassAttribute(startTag, rewrite.classNames)
	}

	classValueStart := -1
	classValueEnd := -1
	for _, pair := range [][2]int{{4, 5}, {6, 7}, {8, 9}} {
		if classMatch[pair[0]] >= 0 && classMatch[pair[1]] >= 0 {
			classValueStart = classMatch[pair[0]]
			classValueEnd = classMatch[pair[1]]
			break
		}
	}
	if classValueStart < 0 || classValueEnd < 0 {
		return startTag
	}

	classValue := startTag[classValueStart:classValueEnd]
	updatedClassValue := removeSiteBrushTemplateClass(classValue)
	if !remove {
		updatedClassValue = mergeClassNames(updatedClassValue, rewrite.classNames)
	}
	if strings.TrimSpace(updatedClassValue) == "" {
		return startTag[:classMatch[0]] + startTag[classMatch[1]:]
	}
	return startTag[:classMatch[0]] + ` class="` + updatedClassValue + `"` + startTag[classMatch[1]:]
}

func insertClassAttribute(startTag string, classNames []string) string {
	insertIndex := strings.LastIndex(startTag, ">")
	if insertIndex < 0 {
		return startTag
	}
	for insertIndex > 0 && unicode.IsSpace(rune(startTag[insertIndex-1])) {
		insertIndex--
	}
	if insertIndex > 0 && startTag[insertIndex-1] == '/' {
		insertIndex--
	}
	return startTag[:insertIndex] + ` class="` + mergeClassNames("", classNames) + `"` + startTag[insertIndex:]
}

func mergeClassNames(existingClassValue string, classNames []string) string {
	seen := make(map[string]struct{})
	merged := make([]string, 0, len(strings.Fields(existingClassValue))+len(classNames)+1)
	merged = append(merged, "SiteBrush-Template")
	seen[strings.ToLower("SiteBrush-Template")] = struct{}{}
	for _, className := range append(classNames, strings.Fields(existingClassValue)...) {
		if strings.EqualFold(className, "SiteBrush-Template") {
			continue
		}
		lowerClassName := strings.ToLower(className)
		if _, exists := seen[lowerClassName]; exists {
			continue
		}
		seen[lowerClassName] = struct{}{}
		merged = append(merged, className)
	}
	return strings.Join(merged, " ")
}

func prependSiteBrushTemplateClass(classValue string) []string {
	classNameList := make([]string, 0, len(strings.Fields(classValue))+1)
	classNameList = append(classNameList, "SiteBrush-Template")
	for _, className := range strings.Fields(classValue) {
		if strings.EqualFold(className, "SiteBrush-Template") {
			continue
		}
		classNameList = append(classNameList, className)
	}
	return classNameList
}

func removeSiteBrushTemplateClass(classValue string) string {
	classNameList := make([]string, 0, len(strings.Fields(classValue)))
	for _, className := range strings.Fields(classValue) {
		if strings.EqualFold(className, "SiteBrush-Template") {
			continue
		}
		classNameList = append(classNameList, className)
	}
	return strings.Join(classNameList, " ")
}

func classListHasSiteBrushTemplate(classValue string) bool {
	for _, className := range strings.Fields(classValue) {
		if strings.EqualFold(className, "SiteBrush-Template") {
			return true
		}
	}
	return false
}

func sortedNonOverlappingMatches(matchList []match) []match {
	if len(matchList) < 2 {
		return matchList
	}
	sortedMatchList := append([]match(nil), matchList...)
	sort.Slice(sortedMatchList, func(leftIndex, rightIndex int) bool {
		leftMatch := sortedMatchList[leftIndex]
		rightMatch := sortedMatchList[rightIndex]
		if leftMatch.start != rightMatch.start {
			return leftMatch.start < rightMatch.start
		}
		return leftMatch.end > rightMatch.end
	})
	filteredMatchList := sortedMatchList[:0]
	previousEnd := -1
	for _, currentMatch := range sortedMatchList {
		if currentMatch.start < previousEnd {
			continue
		}
		filteredMatchList = append(filteredMatchList, currentMatch)
		previousEnd = currentMatch.end
	}
	return filteredMatchList
}

func scanMatches(pageHTML string) []match {
	tokenizer := html.NewTokenizer(strings.NewReader(pageHTML))
	openStack := make([]openElement, 0)
	matchList := make([]match, 0)
	offset := 0
	for {
		tokenType := tokenizer.Next()
		rawToken := tokenizer.Raw()
		tokenStart := offset
		tokenEnd := tokenStart + len(rawToken)
		offset = tokenEnd
		switch tokenType {
		case html.ErrorToken:
			return matchList
		case html.StartTagToken:
			token := tokenizer.Token()
			openStack = append(openStack, openElement{
				tagName:    token.Data,
				start:      tokenStart,
				templateID: templateIdentifierFromAttributes(token.Data, token.Attr),
			})
		case html.SelfClosingTagToken:
			token := tokenizer.Token()
			templateID := templateIdentifierFromAttributes(token.Data, token.Attr)
			if templateID == "" {
				continue
			}
			matchList = append(matchList, match{
				start: tokenStart,
				end:   tokenEnd,
				id:    templateID,
				block: pageHTML[tokenStart:tokenEnd],
			})
		case html.EndTagToken:
			token := tokenizer.Token()
			for len(openStack) > 0 {
				lastIndex := len(openStack) - 1
				openElement := openStack[lastIndex]
				openStack = openStack[:lastIndex]
				if !strings.EqualFold(openElement.tagName, token.Data) {
					continue
				}
				if openElement.templateID == "" {
					break
				}
				matchList = append(matchList, match{
					start: openElement.start,
					end:   tokenEnd,
					id:    openElement.templateID,
					block: pageHTML[openElement.start:tokenEnd],
				})
				break
			}
		}
	}
}

func templateIdentifierFromAttributes(tagName string, attributeList []html.Attribute) string {
	for _, attribute := range attributeList {
		if !strings.EqualFold(attribute.Key, "class") {
			continue
		}
		identifier := templateIdentifierFromClassList(attribute.Val)
		if identifier == "" {
			return ""
		}
		return strings.ToLower(strings.TrimSpace(tagName)) + "\x00" + identifier
	}
	return ""
}

func templateIdentifierFromClassList(classValue string) string {
	classNameList := strings.Fields(classValue)
	hasTemplateClass := false
	for _, className := range classNameList {
		if strings.EqualFold(className, "SiteBrush-Template") {
			hasTemplateClass = true
			continue
		}
		lowerClassName := strings.ToLower(className)
		if strings.HasPrefix(lowerClassName, "sitebrush-template-") {
			return className[len("sitebrush-template-"):]
		}
	}
	if hasTemplateClass {
		return normalizedTemplateIdentifierClassList(classNameList)
	}
	return ""
}

func normalizedTemplateIdentifierClassList(classNameList []string) string {
	identifierClassSet := make(map[string]struct{})
	for _, className := range classNameList {
		if strings.EqualFold(className, "SiteBrush-Template") {
			continue
		}
		identifierClassSet[className] = struct{}{}
	}
	identifierClassList := make([]string, 0, len(identifierClassSet))
	for className := range identifierClassSet {
		identifierClassList = append(identifierClassList, className)
	}
	sort.Strings(identifierClassList)
	return strings.Join(identifierClassList, " ")
}
