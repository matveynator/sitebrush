package publicsite

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/matveynator/sitebrush/v2/pkg/crawler"
	"golang.org/x/net/html"
)

type ramCandidate struct {
	generation                                               *publicGeneration
	response                                                 *preparedRAMFile
	pathname, domain, directory, relative, version, identity string
	key                                                      HTMLVariantKey
	size, insertion                                          int64
	bootstrap                                                string
	priority, uses                                           int
}

func (runtime *PublicRuntime) ramSource(policy Policy, pathname, resource string) (string, string) {
	if resource != "" {
		if policy.Frozen {
			return "versions", policy.FileVersions[resource]
		}
		return "files", resource
	}
	if version := policy.PublishedVersions[pathname]; version != "" {
		return "versions", version
	}
	return "static", runtime.options.StaticPath(pathname)
}

// Only publication workers inspect dependencies. Traversal is local and bounded;
// external references never become fetches or admission authority.
func readRAMReferences(file *os.File, document bool, stop <-chan struct{}) []string {
	references := make([]string, 0)
	remember := func(reference string) string {
		if len(references) < 4096 {
			references = append(references, reference)
		}
		return reference
	}
	source := preparationReader{source: io.NewSectionReader(file, 0, fileSize(file)), stop: stop}
	if document {
		tokenizer := html.NewTokenizer(source)
		tokenizer.SetMaxBuf(1 << 20)
		inStyle := false
		for len(references) < 4096 {
			switch tokenizer.Next() {
			case html.ErrorToken:
				return references
			case html.StartTagToken, html.SelfClosingTagToken:
				tag := string(tokenizer.Raw())
				crawler.RewriteDocumentResourceReferences(tag, remember)
				name, _ := tokenizer.TagName()
				inStyle = string(name) == "style"
			case html.EndTagToken:
				inStyle = false
			case html.TextToken:
				if inStyle {
					crawler.RewriteCSSImportReferences(string(tokenizer.Raw()), remember)
					crawler.RewriteCSSURLReferences(string(tokenizer.Raw()), remember)
				}
			}
		}
	} else {
		content, err := io.ReadAll(io.LimitReader(source, MaximumRAMRepresentationBytes))
		if err != nil {
			return nil
		}
		crawler.RewriteCSSImportReferences(string(content), remember)
		crawler.RewriteCSSURLReferences(string(content), remember)
	}
	return references
}

func fileSize(file *os.File) int64 {
	info, err := file.Stat()
	if err != nil {
		return 0
	}
	return info.Size()
}

func dependencyRoute(domain, pathname, reference string) string {
	parsed, err := url.Parse(strings.TrimSpace(reference))
	if err != nil {
		return ""
	}
	resolved := (&url.URL{Scheme: "https", Host: domain, Path: "/" + strings.TrimPrefix(pathname, "/")}).ResolveReference(parsed)
	if resolved.Scheme != "https" && resolved.Scheme != "http" || !strings.EqualFold(resolved.Host, domain) {
		return ""
	}
	return resolved.Path
}

func dependencyUses(generation *publicGeneration, policy Policy) map[string]int {
	uses := make(map[string]int)
	sources := make(map[string]bool)
	paths := make([]string, 0, len(generation.pageSources))
	for pathname := range generation.pageSources {
		paths = append(paths, pathname)
	}
	sort.Strings(paths)
	for _, pathname := range paths {
		source := generation.pageSources[pathname]
		if sources[source] {
			continue
		}
		sources[source] = true
		visited := make(map[string]bool)
		counted := make(map[string]bool)
		pending := []string{pathname}
		for len(pending) != 0 && len(visited) < 4096 {
			parent := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			for _, reference := range generation.references[parent] {
				route := dependencyRoute(policy.Domain, parent, reference)
				if route == "" || visited[route] {
					continue
				}
				visited[route] = true
				name := policy.ResourcePaths[route]
				if name == "" {
					name = route
				}
				if !counted[name] {
					uses[name]++
					counted[name] = true
				}
				// Follow CSS imports/fonts, without counting a shared dependency twice per page.
				response := generation.ram[route]
				if strings.HasSuffix(strings.ToLower(route), ".css") || response != nil && strings.Contains(response.contentType, "text/css") {
					pending = append(pending, route)
				}
				if len(visited) >= 4096 {
					break
				}
			}
		}
	}
	return uses
}

// All sites compete in one ordered admission pass. Landing pages and shared
// dependencies precede rare files and alternate/updating HTML representations.
// Published maps never acquire an LRU or mutate in response to a visitor.
func (runtime *PublicRuntime) admitRAM(generations map[string]*publicGeneration, policies map[string]cachedPolicy, stop <-chan struct{}) error {
	var candidates []ramCandidate
	for domain, generation := range generations {
		uses := dependencyUses(generation, policies[domain].policy)
		for _, candidate := range generation.pendingRAM {
			candidate.uses = uses[candidate.pathname]
			candidate.priority = 6
			if candidate.response.html {
				candidate.priority = 5
				if candidate.key.Language == runtime.options.DefaultLanguage || runtime.options.DefaultLanguage == "" {
					candidate.priority = 4
					if candidate.pathname == "/" {
						candidate.priority = 0
					}
				} else if candidate.pathname == "/" {
					candidate.priority = 2
				}
				if candidate.key.Updating {
					candidate.priority = 7
				}
			} else if candidate.uses > 1 {
				candidate.priority = 1
			} else if candidate.uses > 0 {
				candidate.priority = 3
			}
			candidates = append(candidates, candidate)
		}
		generation.pendingRAM = nil
	}
	sort.Slice(candidates, func(first, second int) bool {
		left, right := candidates[first], candidates[second]
		if left.priority != right.priority {
			return left.priority < right.priority
		}
		if left.uses != right.uses {
			return left.uses > right.uses
		}
		if left.key.Menu != right.key.Menu {
			return left.key.Menu
		}
		if left.size != right.size {
			return left.size < right.size
		}
		if left.domain != right.domain {
			return left.domain < right.domain
		}
		if left.pathname != right.pathname {
			return left.pathname < right.pathname
		}
		if left.key.Language != right.key.Language {
			return left.key.Language < right.key.Language
		}
		return !left.key.Updating && right.key.Updating
	})
	for _, candidate := range candidates {
		select {
		case <-stop:
			return errors.New("RAM preparation stopped")
		default:
		}
		body, err := runtime.retainRAMBody(candidate.generation, candidate.identity, candidate.size, stop, candidate.readBody)
		if err != nil {
			return err
		}
		if body != nil {
			candidate.response.variants[candidate.key] = body
		}
	}
	for _, generation := range generations {
		generation.references = nil
		generation.pageSources = nil
	}
	return nil
}

func (candidate ramCandidate) readBody(payload []byte) error {
	root := candidate.generation.roots[candidate.directory]
	if root == nil {
		return os.ErrNotExist
	}
	file, openErr := root.Open(candidate.relative)
	if openErr != nil {
		return openErr
	}
	defer file.Close()
	position := candidate.insertion
	if _, readErr := io.ReadFull(io.NewSectionReader(file, 0, position), payload[:position]); readErr != nil {
		return readErr
	}
	copy(payload[position:], candidate.bootstrap)
	suffix := payload[position+int64(len(candidate.bootstrap)):]
	if _, readErr := io.ReadFull(io.NewSectionReader(file, position, int64(len(suffix))), suffix); readErr != nil {
		return readErr
	}
	digest := sha256.New()
	digest.Write(payload[:position])
	digest.Write(suffix)
	if hex.EncodeToString(digest.Sum(nil)) != candidate.version {
		return errors.New("public source changed during RAM admission")
	}
	return nil
}
