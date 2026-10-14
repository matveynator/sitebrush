package analytics

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// The authored page keeps structural keys computed before unsafe nodes are
// removed. A sandboxed preview has no executable page code or form contents.
func BehaviourPreview(reader io.Reader, pagePaths ...string) (string, error) {
	root, err := html.Parse(io.LimitReader(reader, 2<<20))
	if err != nil {
		return "", err
	}
	layout := previewLayout(root)
	var walk func(*html.Node, string)
	walk = func(parent *html.Node, prefix string) {
		ordinals := map[string]int{}
		for node := parent.FirstChild; node != nil; {
			next := node.NextSibling
			if node.Type == html.ElementNode {
				ordinals[node.Data]++
				key := node.Data + ":" + strconv.Itoa(ordinals[node.Data])
				if prefix != "" {
					key = prefix + ">" + key
				}
				for _, attribute := range node.Attr {
					if attribute.Key == "data-analytics-key" && safeEventName(attribute.Val) {
						key = "key:" + attribute.Val
					}
				}
				switch node.Data {
				case "script", "iframe", "object", "embed", "meta", "base":
					parent.RemoveChild(node)
					node = next
					continue
				}
				attributes := []html.Attribute{}
				for _, attribute := range node.Attr {
					if strings.HasPrefix(strings.ToLower(attribute.Key), "on") {
						continue
					}
					switch attribute.Key {
					case "href":
						if node.Data != "link" {
							continue
						}
					case "value", "srcdoc", "action", "formaction", "contenteditable", "autofocus", "name":
						continue
					}
					if len(pagePaths) > 0 && (attribute.Key == "href" || attribute.Key == "src") {
						if address, parseErr := url.Parse(attribute.Val); parseErr == nil {
							base := &url.URL{Path: pagePaths[0]}
							attribute.Val = base.ResolveReference(address).String()
						}
					}
					attributes = append(attributes, attribute)
				}
				attributes = append(attributes, html.Attribute{Key: "data-preview-key", Val: previewStructuralKey(node)})
				if node.Data == "html" {
					attributes = append(attributes, html.Attribute{Key: "data-analytics-layout", Val: layout})
				}
				node.Attr = attributes
				if node.Data == "textarea" {
					for node.FirstChild != nil {
						node.RemoveChild(node.FirstChild)
					}
				} else {
					walk(node, key)
				}
			}
			node = next
		}
	}
	walk(root, "")
	var result bytes.Buffer
	if err := html.Render(&result, root); err != nil {
		return "", fmt.Errorf("render preview: %w", err)
	}
	return result.String(), nil
}

// Layout fingerprints include structural positions only, matching the collector.
func previewLayout(root *html.Node) string {
	signature := uint32(2166136261)
	publishedLayout := ""
	observed := 0
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, insideBody bool) {
		if node.Type == html.ElementNode && node.Data == "script" {
			owned := false
			layout := ""
			for _, attribute := range node.Attr {
				if attribute.Key == "data-sitebrush-owned" && attribute.Val == "true" {
					owned = true
				}
				if attribute.Key == "data-analytics-layout" {
					layout = attribute.Val
				}
			}
			if owned && len(layout) == 16 {
				if decoded, err := hex.DecodeString(layout); err == nil && len(decoded) == 8 {
					publishedLayout = layout
				}
			}
		}
		if node.Type == html.ElementNode && node.Data == "body" {
			insideBody = true
		} else if insideBody && node.Type == html.ElementNode && observed < 1000 {
			observed++
			excluded := node.Data == "script" || node.Data == "style"
			for _, attribute := range node.Attr {
				if attribute.Key == "data-sitebrush-admin" {
					excluded = true
				}
			}
			if excluded {
				return
			}
			key := previewStructuralKey(node)
			if len(key) > 180 {
				key = ""
			}
			for _, character := range key {
				signature = (signature ^ uint32(character)) * 16777619
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, insideBody)
		}
	}
	walk(root, false)
	if publishedLayout != "" {
		return publishedLayout
	}
	return strconv.FormatUint(uint64(signature), 16)
}
func previewStructuralKey(node *html.Node) string {
	for _, attribute := range node.Attr {
		if attribute.Key == "data-analytics-key" && safeEventName(attribute.Val) {
			return "key:" + attribute.Val
		}
	}
	segments := []string{}
	for current := node; current != nil && current.Type == html.ElementNode && len(segments) < 12; current = current.Parent {
		ordinal := 1
		for previous := current.PrevSibling; previous != nil; previous = previous.PrevSibling {
			if previous.Type == html.ElementNode && previous.Data == current.Data {
				ordinal++
			}
		}
		segments = append([]string{current.Data + ":" + strconv.Itoa(ordinal)}, segments...)
	}
	return strings.Join(segments, ">")
}
