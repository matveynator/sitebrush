package analytics

import (
	"path"
	"sort"
	"strings"
)

// Published structure and observed navigation remain separate: URL nesting
// describes the site, while each page's traffic describes actual journeys.
type PublishedPage struct{ Path, Title string }
type SiteTree struct {
	Root        *SiteTreeNode
	Truncated   bool
	Unavailable bool
}
type SiteTreeNode struct {
	Path, Title string
	Published   bool
	Traffic     *ConversionPath
	Children    []*SiteTreeNode
}

func BuildSiteTree(pages []PublishedPage, journeys []*ConversionPath) SiteTree {
	truncated := false
	nodes := map[string]*SiteTreeNode{"/": {Path: "/", Title: "/"}}
	var ensureNode func(string) *SiteTreeNode
	ensureNode = func(pagePath string) *SiteTreeNode {
		if existing := nodes[pagePath]; existing != nil {
			return existing
		}
		if len(nodes) >= 4096 {
			truncated = true
			return nil
		}
		created := &SiteTreeNode{Path: pagePath, Title: pagePath}
		nodes[pagePath] = created
		parentPath := path.Dir(pagePath)
		if strings.HasSuffix(pagePath, "/") {
			parentPath = strings.TrimSuffix(pagePath, "/")
		}
		parent := ensureNode(parentPath)
		if parent == nil {
			delete(nodes, pagePath)
			return nil
		}
		parent.Children = append(parent.Children, created)
		return created
	}
	for _, page := range pages {
		if SafePath(page.Path) != page.Path || !strings.HasPrefix(page.Path, "/") || path.Clean(page.Path) == "." {
			continue
		}
		node := ensureNode(page.Path)
		if node == nil {
			continue
		}
		node.Published = true
		if strings.TrimSpace(page.Title) != "" {
			node.Title = CleanText(page.Title, 160)
		}
	}
	for _, journey := range journeys {
		if SafePath(journey.Root) != journey.Root || !strings.HasPrefix(journey.Root, "/") {
			continue
		}
		if node := ensureNode(journey.Root); node != nil {
			node.Traffic = journey
		}
	}
	var sortChildren func(*SiteTreeNode)
	sortChildren = func(node *SiteTreeNode) {
		sort.Slice(node.Children, func(left, right int) bool { return node.Children[left].Path < node.Children[right].Path })
		for _, child := range node.Children {
			sortChildren(child)
		}
	}
	sortChildren(nodes["/"])
	return SiteTree{Root: nodes["/"], Truncated: truncated}
}
