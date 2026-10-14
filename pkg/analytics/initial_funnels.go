package analytics

import (
	"net/url"
	"path"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

type PublishedAction struct{ Kind, Element, Name string }

// Only authored, published structure informs suggestions. No visitor text,
// guessed business outcomes or URL-parent relationships become funnel steps.
func InspectPublishedPage(page PublishedPage, document, domain string) PublishedPage {
	if len(document) > 65536 {
		return page
	}
	root, err := html.Parse(strings.NewReader(document))
	if err != nil {
		return page
	}
	page.Inspected = true
	links := map[string]bool{}
	var walk func(*html.Node)
	walk = func(element *html.Node) {
		if element.Type == html.ElementNode {
			attributes := map[string]string{}
			for _, attribute := range element.Attr {
				attributes[attribute.Key] = attribute.Val
			}
			key := previewStructuralKey(element)
			if element.Data == "form" && safeStructuralKey(key) && len(page.Actions) < 16 {
				page.Actions = append(page.Actions, PublishedAction{Kind: "form-submit", Element: key})
			}
			if element.Data == "a" {
				target, parseErr := url.Parse(attributes["href"])
				if parseErr == nil && (target.Scheme == "" || target.Scheme == "https" || target.Scheme == "http") && target.Path != "" {
					base := &url.URL{Path: page.Path}
					pathname := SafePath(base.ResolveReference(target).Path)
					if (target.Host == "" || strings.EqualFold(target.Hostname(), domain)) && pathname != "" && len(links) < 100 {
						links[pathname] = true
					}
					_, downloadAttribute := attributes["download"]
					if (downloadAttribute || publishedDownloadPath(pathname)) && safeStructuralKey(key) && len(page.Actions) < 16 {
						page.Actions = append(page.Actions, PublishedAction{Kind: "download", Element: key, Name: publishedActionText(element)})
					}
				}
			}
		}
		for child := element.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	for link := range links {
		page.Links = append(page.Links, link)
	}
	sort.Strings(page.Links)
	return page
}

func publishedDownloadPath(pathname string) bool {
	switch strings.ToLower(path.Ext(pathname)) {
	case ".zip", ".gz", ".tgz", ".tar", ".bz2", ".xz", ".7z", ".rar", ".dmg", ".exe", ".deb", ".rpm", ".pdf", ".epub", ".msi", ".apk", ".pkg", ".appimage", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx":
		return true
	}
	return false
}

func publishedActionText(element *html.Node) string {
	var text strings.Builder
	var collect func(*html.Node)
	collect = func(child *html.Node) {
		if child.Type == html.ElementNode {
			switch child.Data {
			case "script", "style", "input", "textarea", "select", "option":
				return
			}
		}
		if child.Type == html.TextNode {
			text.WriteString(child.Data)
		}
		for nested := child.FirstChild; nested != nil; nested = nested.NextSibling {
			collect(nested)
		}
	}
	collect(element)
	return CleanText(strings.Join(strings.Fields(text.String()), " "), 64)
}

// This priority keeps initialization small and deterministic. Unknown sites get
// a page/scroll goal; a form submission is named as an attempt, never a success.
func PublishedPagePriority(page PublishedPage) int {
	text := strings.ToLower(page.Path + " " + page.Title)
	for _, keyword := range []string{"download", "скач", "contact", "контакт", "register", "signup", "регист", "pricing", "тариф", "checkout", "заказ", "product", "продукт"} {
		if strings.Contains(text, keyword) {
			return 2
		}
	}
	if page.Path == "/" {
		return 3
	}
	return 1
}

func InitialGoals(pages []PublishedPage, translations ...map[string]string) []Goal {
	label := func(key, fallback string) string {
		if len(translations) > 0 && translations[0][key] != "" {
			return translations[0][key]
		}
		return fallback
	}
	ordered := []PublishedPage{}
	for _, page := range pages {
		if len(page.Path) <= 256 && SafePath(page.Path) == page.Path {
			ordered = append(ordered, page)
		}
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		if PublishedPagePriority(ordered[left]) != PublishedPagePriority(ordered[right]) {
			return PublishedPagePriority(ordered[left]) > PublishedPagePriority(ordered[right])
		}
		return ordered[left].Path < ordered[right].Path
	})
	goals := []Goal{}
	seen := map[string]bool{}
	names := map[string]bool{}
	appendGoal := func(goal Goal) {
		key := GoalKey(goal)
		if len(goals) < 4 && !seen[key] {
			goal.Name = strings.ToValidUTF8(CleanText(goal.Name, 48), "")
			if names[goal.Name] {
				goal.Name += " · " + key[:6]
			}
			names[goal.Name] = true
			goal.Automatic = true
			goal.Primary = len(goals) == 0
			goals = append(goals, goal)
			seen[key] = true
		}
	}
	for _, page := range ordered {
		if len(goals) >= 4 {
			break
		}
		for _, action := range page.Actions {
			if len(goals) >= 4 {
				break
			}
			steps := publishedFunnelRoute(pages, page.Path)
			name := action.Name
			if name == "" {
				name = page.Title
			}
			if name == "" {
				name = page.Path
			}
			actionName := label("cx_kind_"+strings.ReplaceAll(action.Kind, "-", "_"), action.Kind)
			steps = append(steps, Goal{Name: actionName, Kind: action.Kind, Page: page.Path, Element: action.Element})
			appendGoal(Goal{Name: name + " · " + actionName, Kind: action.Kind, Page: page.Path, Element: action.Element})
			if len(steps) > 2 {
				appendGoal(Goal{Name: name + " · " + label("ax_journeys", "Route"), Steps: steps})
			}
		}
	}
	for _, page := range ordered {
		if len(goals) >= 4 {
			break
		}
		if page.Path == "/" || len(page.Actions) > 0 || PublishedPagePriority(page) < 2 {
			continue
		}
		name := page.Title
		if name == "" {
			name = page.Path
		}
		appendGoal(Goal{Name: name, Kind: "page", Page: page.Path})
	}
	if len(goals) == 0 && len(ordered) > 0 {
		page := ordered[0]
		steps := publishedFunnelRoute(pages, page.Path)
		steps = append(steps, Goal{Name: label("cx_kind_scroll", "Scroll") + " 75%", Kind: "scroll", Page: page.Path, Threshold: 75})
		appendGoal(Goal{Name: page.Path + " · " + label("cx_kind_scroll", "Scroll") + " 75%", Steps: steps})
	}
	return goals
}

func publishedFunnelRoute(pages []PublishedPage, target string) []Goal {
	byPath := map[string]PublishedPage{}
	for _, page := range pages {
		byPath[page.Path] = page
	}
	route := []string{target}
	if _, exists := byPath["/"]; exists && target != "/" {
		queue := [][]string{{"/"}}
		visited := map[string]bool{"/": true}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			last := current[len(current)-1]
			if last == target {
				route = current
				break
			}
			if len(current) >= 4 {
				continue
			}
			for _, next := range byPath[last].Links {
				if _, exists := byPath[next]; !exists || visited[next] {
					continue
				}
				visited[next] = true
				queue = append(queue, append(append([]string(nil), current...), next))
			}
		}
	}
	steps := []Goal{}
	for _, pathname := range route {
		name := byPath[pathname].Title
		if name == "" {
			name = pathname
		}
		steps = append(steps, Goal{Name: strings.ToValidUTF8(CleanText(name, 64), ""), Kind: "page", Page: pathname})
	}
	return steps
}
