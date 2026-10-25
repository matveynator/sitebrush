// Package revisioncontent normalizes HTML source for revision identity.
package revisioncontent

import (
	"crypto/sha256"
	"hash"
	"io"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// HTMLHash ignores HTML markup formatting while retaining attributes, comments,
// and whitespace where HTML or embedded code can give it meaning.
func HTMLHash(source string) [sha256.Size]byte {
	document, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return sha256.Sum256([]byte(source))
	}
	digest := sha256.New()
	writeNode(digest, document, false)
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func writeNode(digest hash.Hash, node *html.Node, preserveWhitespace bool) {
	switch node.Type {
	case html.DocumentNode:
		writeField(digest, "document", "")
	case html.DoctypeNode:
		writeField(digest, "doctype", node.Data)
	case html.CommentNode:
		writeField(digest, "comment", node.Data)
	case html.ElementNode:
		writeField(digest, "element", node.Namespace+":"+node.Data)
		attributes := append([]html.Attribute(nil), node.Attr...)
		sort.Slice(attributes, func(left, right int) bool {
			leftAttribute := attributes[left]
			rightAttribute := attributes[right]
			if leftAttribute.Namespace != rightAttribute.Namespace {
				return leftAttribute.Namespace < rightAttribute.Namespace
			}
			if leftAttribute.Key != rightAttribute.Key {
				return leftAttribute.Key < rightAttribute.Key
			}
			return leftAttribute.Val < rightAttribute.Val
		})
		for _, attribute := range attributes {
			writeField(digest, "attribute", attribute.Namespace+":"+attribute.Key)
			writeField(digest, "attribute-value", attribute.Val)
		}
		preserveWhitespace = preserveWhitespace || elementPreservesWhitespace(node)
	case html.TextNode:
		text := node.Data
		if !preserveWhitespace {
			text = normalizedText(node)
		}
		if text == "" {
			return
		}
		writeField(digest, "text", text)
		return
	default:
		writeField(digest, "other", strconv.Itoa(int(node.Type))+":"+node.Data)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		writeNode(digest, child, preserveWhitespace)
	}
	writeField(digest, "end", "")
}

func writeField(digest hash.Hash, kind, content string) {
	_, _ = io.WriteString(digest, kind)
	_, _ = io.WriteString(digest, ":")
	_, _ = io.WriteString(digest, strconv.Itoa(len(content)))
	_, _ = io.WriteString(digest, ":")
	_, _ = io.WriteString(digest, content)
}

func elementPreservesWhitespace(node *html.Node) bool {
	switch node.Data {
	case "pre", "textarea", "script", "style", "code", "svg", "math":
		return true
	}
	for _, attribute := range node.Attr {
		if attribute.Key == "style" && strings.Contains(strings.ToLower(attribute.Val), "white-space") {
			return true
		}
		if attribute.Namespace == "xml" && attribute.Key == "space" && attribute.Val == "preserve" {
			return true
		}
	}
	return false
}

func normalizedText(node *html.Node) string {
	text := collapseHTMLSpaces(node.Data)
	if text == " " && layoutWhitespace(node) {
		return ""
	}
	if node.Parent != nil && blockElement(node.Parent.Data) {
		if node.PrevSibling == nil {
			text = strings.TrimPrefix(text, " ")
		}
		if node.NextSibling == nil {
			text = strings.TrimSuffix(text, " ")
		}
	}
	return text
}

func collapseHTMLSpaces(source string) string {
	var result strings.Builder
	result.Grow(len(source))
	previousSpace := false
	for index := 0; index < len(source); index++ {
		character := source[index]
		isSpace := character == ' ' || character == '\t' || character == '\n' || character == '\r' || character == '\f'
		if isSpace {
			if !previousSpace {
				result.WriteByte(' ')
			}
		} else {
			result.WriteByte(character)
		}
		previousSpace = isSpace
	}
	return result.String()
}

func layoutWhitespace(node *html.Node) bool {
	if node.Parent == nil {
		return false
	}
	if node.Parent.Data == "head" {
		return true
	}
	return blockElement(node.Parent.Data) && blockSibling(node.PrevSibling) && blockSibling(node.NextSibling)
}

func blockSibling(node *html.Node) bool {
	return node == nil || node.Type == html.ElementNode && blockElement(node.Data)
}

func blockElement(tag string) bool {
	switch tag {
	case "address", "article", "aside", "blockquote", "body", "caption", "dd", "div", "dl", "dt", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "head", "header", "hr", "html", "li", "main", "nav", "ol", "p", "section", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul":
		return true
	}
	return false
}
