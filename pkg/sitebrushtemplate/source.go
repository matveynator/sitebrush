package sitebrushtemplate

import (
	"io"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

type sourceElement struct {
	start    int
	startEnd int
	paired   bool
	valid    bool
}

var detectionClassAttributePattern = regexp.MustCompile(`(?is)\sclass(?:\s*=\s*("([^"]*)"|'([^']*)'|([^\s"'=<>]*)))?(\s|/?>)`)

// Prefix the actual class attribute, preserving its original spelling and
// quoting. Attribute-like text inside another quoted attribute is not markup.
func markDetectionStartTag(startTag string, templateClasses []string) string {
	quoteCharacter := byte(0)
	checkedOffset := 0
	for checkedOffset < len(startTag) {
		classMatch := detectionClassAttributePattern.FindStringSubmatchIndex(startTag[checkedOffset:])
		if classMatch == nil {
			break
		}
		for index := range classMatch {
			if classMatch[index] >= 0 {
				classMatch[index] += checkedOffset
			}
		}
		for checkedOffset < classMatch[0] {
			character := startTag[checkedOffset]
			if quoteCharacter == 0 && (character == '\'' || character == '"') {
				quoteCharacter = character
			} else if quoteCharacter == character {
				quoteCharacter = 0
			}
			checkedOffset++
		}
		if quoteCharacter != 0 {
			checkedOffset++
			continue
		}
		prefix := strings.Join(templateClasses, " ") + " "
		for _, attributeRange := range [][2]int{{4, 5}, {6, 7}, {8, 9}} {
			if classMatch[attributeRange[0]] < 0 {
				continue
			}
			start := classMatch[attributeRange[0]]
			end := classMatch[attributeRange[1]]
			if attributeRange[0] == 8 {
				return startTag[:start] + `"` + prefix + startTag[start:end] + `"` + startTag[end:]
			}
			return startTag[:start] + prefix + startTag[start:]
		}
		attributeNameStart := classMatch[0]
		for strings.ContainsRune(" \t\r\n\f", rune(startTag[attributeNameStart])) {
			attributeNameStart++
		}
		attributeNameEnd := attributeNameStart + len("class")
		return startTag[:attributeNameEnd] + `="` + strings.TrimSpace(prefix) + `"` + startTag[attributeNameEnd:]
	}
	return insertClassAttribute(startTag, templateClasses)
}

// Detection follows source boundaries rather than HTML parser repairs. A
// repaired element cannot safely define the source range of a shared template.
func parseDetectionSource(sourceHTML string) (*html.Node, map[*html.Node]*sourceElement, error) {
	root := &html.Node{Type: html.DocumentNode}
	spans := make(map[*html.Node]*sourceElement)
	openNodes := []*html.Node{root}
	tokenizer := html.NewTokenizer(strings.NewReader(sourceHTML))
	offset := 0
	for {
		tokenType := tokenizer.Next()
		tokenStart := offset
		offset += len(tokenizer.Raw())
		if tokenType == html.ErrorToken {
			if tokenizer.Err() != io.EOF {
				return nil, nil, tokenizer.Err()
			}
			for _, node := range openNodes[1:] {
				spans[node].valid = false
			}
			return root, spans, nil
		}
		token := tokenizer.Token()
		parent := openNodes[len(openNodes)-1]
		switch tokenType {
		case html.StartTagToken, html.SelfClosingTagToken:
			node := &html.Node{Type: html.ElementNode, Data: token.Data, Attr: token.Attr}
			parent.AppendChild(node)
			span := &sourceElement{start: tokenStart, startEnd: offset, valid: true}
			spans[node] = span
			if isHTMLVoidElement(token.Data) {
				continue
			}
			if tokenType == html.SelfClosingTagToken {
				// Foreign graphic leaves are content, but never template roots.
				span.valid = insideSVG(node)
				continue
			}
			openNodes = append(openNodes, node)
		case html.EndTagToken:
			matchedIndex := -1
			for nodeIndex := len(openNodes) - 1; nodeIndex > 0; nodeIndex-- {
				if strings.EqualFold(openNodes[nodeIndex].Data, token.Data) {
					matchedIndex = nodeIndex
					break
				}
			}
			if matchedIndex < 0 {
				if parent != root {
					spans[parent].valid = false
				}
				continue
			}
			matchedSpan := spans[openNodes[matchedIndex]]
			matchedSpan.paired = true
			if matchedIndex != len(openNodes)-1 {
				matchedSpan.valid = false
				for _, unclosedNode := range openNodes[matchedIndex+1:] {
					spans[unclosedNode].valid = false
				}
			}
			openNodes = openNodes[:matchedIndex]
		case html.TextToken:
			parent.AppendChild(&html.Node{Type: html.TextNode, Data: token.Data})
		case html.CommentToken:
			parent.AppendChild(&html.Node{Type: html.CommentNode, Data: token.Data})
		}
	}
}

func insideSVG(node *html.Node) bool {
	for ancestor := node; ancestor != nil; ancestor = ancestor.Parent {
		if strings.EqualFold(ancestor.Data, "svg") {
			return true
		}
	}
	return false
}

func detectionNodeHasContent(node *html.Node) bool {
	if node.Type == html.TextNode {
		return hasMeaningfulText(node.Data)
	}
	if node.Type != html.ElementNode {
		return false
	}
	switch strings.ToLower(node.Data) {
	case "style":
		return hasMeaningfulText(normalizedCSS(elementText(node)))
	case "script":
		return hasJavaScriptContent(elementText(node))
	case "img":
		return hasSourceAttribute(node, "src", "srcset")
	case "path":
		return insideSVG(node) && hasSourceAttribute(node, "d")
	case "polygon", "polyline":
		return insideSVG(node) && hasSourceAttribute(node, "points")
	case "circle":
		return insideSVG(node) && hasSourceAttribute(node, "r")
	case "ellipse":
		return insideSVG(node) && hasSourceAttribute(node, "rx", "ry")
	case "rect":
		return insideSVG(node) && hasSourceAttribute(node, "width", "height")
	case "line":
		return insideSVG(node) && hasSourceAttribute(node, "x2", "y2")
	case "image", "use":
		return insideSVG(node) && hasSourceAttribute(node, "href", "xlink:href")
	}
	return false
}

func hasMeaningfulText(sourceText string) bool {
	return strings.TrimFunc(sourceText, func(character rune) bool {
		return unicode.IsSpace(character) || character == '\u200b' || character == '\ufeff'
	}) != ""
}

func hasSourceAttribute(node *html.Node, attributeNames ...string) bool {
	for _, attribute := range node.Attr {
		for _, attributeName := range attributeNames {
			if strings.EqualFold(attribute.Key, attributeName) && hasMeaningfulText(attribute.Val) {
				return true
			}
		}
	}
	return false
}

// Only the first code token is needed to distinguish content from comments,
// so strings and regular expressions do not require a JavaScript parser.
func hasJavaScriptContent(sourceCode string) bool {
	remainingCode := strings.TrimSpace(sourceCode)
	for remainingCode != "" {
		switch {
		case strings.HasPrefix(remainingCode, "//"), strings.HasPrefix(remainingCode, "<!--"), strings.HasPrefix(remainingCode, "-->"), strings.HasPrefix(remainingCode, "#!"):
			lineEnd := strings.IndexAny(remainingCode, "\r\n\u2028\u2029")
			if lineEnd < 0 {
				return false
			}
			remainingCode = strings.TrimSpace(remainingCode[lineEnd:])
		case strings.HasPrefix(remainingCode, "/*"):
			commentEnd := strings.Index(remainingCode[2:], "*/")
			if commentEnd < 0 {
				return false
			}
			remainingCode = strings.TrimSpace(remainingCode[commentEnd+4:])
		default:
			return hasMeaningfulText(remainingCode)
		}
	}
	return false
}
