// Package pagearchive reads browser saves without making network requests.
// Persistence, publication and quotas remain owned by the application.
package pagearchive

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"path"
	"strings"

	"github.com/matveynator/sitebrush/v2/pkg/crawler"
	"golang.org/x/net/html"
	"howett.net/plist"
)

const (
	UploadLimit   int64 = 512 << 20
	TotalLimit    int64 = 1 << 30
	FileLimit     int64 = 128 << 20
	TextLimit     int64 = 16 << 20
	ResourceLimit       = 50000
	FrameLimit          = 32
)

type Resource struct {
	URL      string
	MIME     string
	Encoding string
	Content  []byte
}

type Archive struct {
	Main      *Resource
	Resources []*Resource
}

func Read(reader io.ReaderAt, size int64, stop <-chan struct{}) (*Archive, error) {
	if size < 1 || size > UploadLimit {
		return nil, errors.New("saved page exceeds upload limit or is empty")
	}
	header := make([]byte, 8)
	_, _ = reader.ReadAt(header, 0)
	if bytes.HasPrefix(header, []byte("PK")) {
		archive, err := zip.NewReader(reader, size)
		if err != nil {
			return nil, err
		}
		return readFirefox(archive, stop)
	}
	return readSafari(io.NewSectionReader(reader, 0, size), stop)
}

func stopped(stop <-chan struct{}) error {
	select {
	case <-stop:
		return errors.New("page archive consumer stopped")
	default:
		return nil
	}
}

func safeName(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") {
		return "", errors.New("unsafe saved page path")
	}
	trimmed := strings.TrimSuffix(name, "/")
	for _, component := range strings.Split(trimmed, "/") {
		if component == ".." || component == "." || component == "" || strings.Contains(component, ":") {
			return "", fmt.Errorf("unsafe saved page path %q", name)
		}
	}
	return trimmed, nil
}

func readFirefox(archive *zip.Reader, stop <-chan struct{}) (*Archive, error) {
	if len(archive.File) > ResourceLimit {
		return nil, errors.New("too many saved page files")
	}
	files := make(map[string]*zip.File)
	var total uint64
	for _, entry := range archive.File {
		if err := stopped(stop); err != nil {
			return nil, err
		}
		name, err := safeName(entry.Name)
		if err != nil {
			return nil, err
		}
		if entry.Mode()&(^entry.Mode().Perm()) != 0 && !entry.FileInfo().IsDir() {
			return nil, errors.New("saved page contains a special file")
		}
		if entry.FileInfo().IsDir() {
			continue
		}
		if _, exists := files[name]; exists {
			return nil, fmt.Errorf("duplicate saved page path %q", name)
		}
		if entry.UncompressedSize64 > uint64(FileLimit) || total > uint64(TotalLimit)-entry.UncompressedSize64 {
			return nil, errors.New("saved page uncompressed size exceeds limit")
		}
		total += entry.UncompressedSize64
		if name == "backup.json" {
			return nil, errors.New("SiteBrush backup must use the backup importer")
		}
		if path.Base(name) == ".DS_Store" || strings.HasPrefix(name, "__MACOSX/") {
			continue
		}
		files[name] = entry
	}
	mainName := ""
	minimumDepth := ResourceLimit
	mainCount := 0
	for name := range files {
		extension := strings.ToLower(path.Ext(name))
		if extension != ".html" && extension != ".htm" {
			continue
		}
		depth := strings.Count(name, "/")
		if depth < minimumDepth {
			minimumDepth, mainCount, mainName = depth, 1, name
		} else if depth == minimumDepth {
			mainCount++
		}
	}
	if mainCount != 1 {
		return nil, errors.New("ZIP must contain one main HTML file and its resource folder")
	}
	result := &Archive{}
	for name, entry := range files {
		if err := stopped(stop); err != nil {
			return nil, err
		}
		limit := FileLimit
		contentType := mime.TypeByExtension(path.Ext(name))
		if name == mainName || textResource(contentType) {
			limit = TextLimit
		}
		if entry.UncompressedSize64 > uint64(limit) {
			return nil, fmt.Errorf("saved page file %q exceeds limit", name)
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(io.LimitReader(stream, limit+1))
		closeErr := stream.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if int64(len(content)) > limit {
			return nil, fmt.Errorf("saved page file %q exceeds limit", name)
		}
		address := (&url.URL{Scheme: "https", Host: "saved-page.invalid", Path: "/" + name}).String()
		resource := &Resource{URL: address, MIME: contentType, Content: content}
		if name == mainName {
			if len(content) == 0 {
				return nil, errors.New("saved page HTML is empty")
			}
			resource.MIME = "text/html"
			result.Main = resource
		} else {
			result.Resources = append(result.Resources, resource)
		}
	}
	return result, nil
}

type safariArchive struct {
	Main      safariResource   `plist:"WebMainResource"`
	Resources []safariResource `plist:"WebSubresources"`
	Frames    []safariArchive  `plist:"WebSubframeArchives"`
}

type safariResource struct {
	Content  []byte `plist:"WebResourceData"`
	URL      string `plist:"WebResourceURL"`
	MIME     string `plist:"WebResourceMIMEType"`
	Encoding string `plist:"WebResourceTextEncodingName"`
}

func readSafari(reader io.ReadSeeker, stop <-chan struct{}) (result *Archive, err error) {
	// The plist decoder reports some malformed binary offsets as panics. This
	// format boundary converts them into an upload error, never an HTTP panic.
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = errors.New("malformed Safari webarchive")
		}
	}()
	if err := stopped(stop); err != nil {
		return nil, err
	}
	var saved safariArchive
	payload, err := io.ReadAll(io.LimitReader(reader, UploadLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > UploadLimit {
		return nil, errors.New("Safari webarchive exceeds upload limit")
	}
	if err := checkPlistLimits(payload, stop); err != nil {
		return nil, err
	}
	decoder := plist.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&saved); err != nil {
		return nil, fmt.Errorf("invalid Safari webarchive: %w", err)
	}
	if decoder.Format != plist.BinaryFormat && decoder.Format != plist.XMLFormat {
		return nil, errors.New("unsupported saved page format")
	}
	result = &Archive{}
	var total int64
	known := make(map[string]*Resource)
	add := func(resource safariResource) (*Resource, error) {
		address, err := url.Parse(resource.URL)
		if err != nil || !address.IsAbs() || (address.Scheme != "http" && address.Scheme != "https" && address.Scheme != "file") {
			return nil, errors.New("invalid resource URL in Safari webarchive")
		}
		address.Fragment = ""
		key := address.String()
		if previous := known[key]; previous != nil {
			if !bytes.Equal(previous.Content, resource.Content) {
				return nil, errors.New("conflicting resources in Safari webarchive")
			}
			return previous, nil
		}
		limit := FileLimit
		if textResource(resource.MIME) {
			limit = TextLimit
		}
		if int64(len(resource.Content)) > limit || total > TotalLimit-int64(len(resource.Content)) {
			return nil, errors.New("Safari webarchive resource size exceeds limit")
		}
		total += int64(len(resource.Content))
		if len(known) >= ResourceLimit {
			return nil, errors.New("too many Safari webarchive resources")
		}
		converted := &Resource{URL: key, MIME: resource.MIME, Encoding: resource.Encoding, Content: resource.Content}
		known[key] = converted
		return converted, nil
	}
	var visit func(safariArchive, int) error
	visit = func(frame safariArchive, depth int) error {
		if err := stopped(stop); err != nil {
			return err
		}
		if depth > FrameLimit {
			return errors.New("too many nested Safari frames")
		}
		main, err := add(frame.Main)
		if err != nil {
			return err
		}
		if !strings.Contains(strings.ToLower(main.MIME), "html") {
			return errors.New("Safari main resource must be HTML")
		}
		if len(main.Content) == 0 {
			return errors.New("Safari main HTML resource is empty")
		}
		if depth == 0 {
			result.Main = main
		} else {
			result.Resources = append(result.Resources, main)
		}
		for _, resource := range frame.Resources {
			converted, err := add(resource)
			if err != nil {
				return err
			}
			result.Resources = append(result.Resources, converted)
		}
		for _, child := range frame.Frames {
			if err := visit(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(saved, 0); err != nil {
		return nil, err
	}
	return result, nil
}

func textResource(contentType string) bool {
	contentType = strings.ToLower(contentType)
	return strings.HasPrefix(contentType, "text/") || strings.Contains(contentType, "javascript") || strings.Contains(contentType, "svg") || strings.Contains(contentType, "xhtml")
}

// Rewrite uses each resource's own base URL. Captured CSS imports and nested
// frames therefore resolve independently of the destination page's path.
func Rewrite(resource *Resource, destinations map[string]string) ([]byte, error) {
	base, err := url.Parse(resource.URL)
	if err != nil {
		return nil, err
	}
	contentType := resource.MIME
	if resource.Encoding != "" {
		contentType += "; charset=" + resource.Encoding
	}
	isHTML := strings.Contains(strings.ToLower(resource.MIME), "html")
	if !textResource(resource.MIME) {
		return resource.Content, nil
	}
	source := crawler.DecodeText(resource.Content, contentType).Text
	if isHTML {
		source = crawler.DecodeHTML(resource.Content, contentType).Text
	}
	originalBase := base
	if isHTML {
		tokens := html.NewTokenizer(strings.NewReader(source))
		for tokens.Next() != html.ErrorToken {
			token := tokens.Token()
			if token.Data == "base" {
				for _, attribute := range token.Attr {
					if attribute.Key == "href" {
						if reference, err := url.Parse(attribute.Val); err == nil {
							base = originalBase.ResolveReference(reference)
						}
						break
					}
				}
				break
			}
		}
	}
	localized := make(map[string]bool, len(destinations))
	for _, destination := range destinations {
		localized[destination] = true
	}
	resolve := func(raw string) string {
		reference, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || strings.HasPrefix(raw, "#") || raw == "" {
			return raw
		}
		withoutFragment := *reference
		withoutFragment.Fragment = ""
		if localized[withoutFragment.String()] {
			return raw
		}
		if reference.Scheme != "" && reference.Scheme != "http" && reference.Scheme != "https" && reference.Scheme != "file" {
			return raw
		}
		resolved := base.ResolveReference(reference)
		fragment := resolved.Fragment
		resolved.Fragment = ""
		if destination, exists := destinations[resolved.String()]; exists {
			if fragment != "" {
				destination += "#" + url.PathEscape(fragment)
			}
			return destination
		}
		// Firefox's saved local paths remain authoritative even if a stale
		// remote <base> was retained in the HTML save.
		local := originalBase.ResolveReference(reference)
		local.Fragment = ""
		if destination, exists := destinations[local.String()]; exists {
			if fragment != "" {
				destination += "#" + url.PathEscape(fragment)
			}
			return destination
		}
		if resolved.Scheme == "file" || resolved.Host == "saved-page.invalid" {
			return raw
		}
		return base.ResolveReference(reference).String()
	}
	if !isHTML {
		if strings.Contains(strings.ToLower(resource.MIME), "css") && strings.HasPrefix(strings.ToLower(strings.TrimSpace(source)), "@charset") {
			if separator := strings.IndexByte(source, ';'); separator >= 0 {
				source = `@charset "utf-8";` + source[separator+1:]
			}
		}
		return []byte(crawler.RewriteDocumentResourceReferences(source, resolve)), nil
	}
	tokens := html.NewTokenizer(strings.NewReader(source))
	var rewritten strings.Builder
	for {
		kind := tokens.Next()
		if kind == html.ErrorToken {
			if tokens.Err() != io.EOF {
				return nil, tokens.Err()
			}
			break
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			rewritten.Write(tokens.Raw())
			continue
		}
		token := tokens.Token()
		if token.Data == "base" {
			continue
		}
		for index := range token.Attr {
			attribute := &token.Attr[index]
			switch attribute.Key {
			case "href", "src", "poster", "data", "action", "data-src", "data-original", "data-original-src", "data-lazy-src", "data-background-image":
				attribute.Val = resolve(attribute.Val)
			case "charset":
				if token.Data == "meta" {
					attribute.Val = "utf-8"
				}
			case "content":
				if token.Data == "meta" && strings.Contains(strings.ToLower(attribute.Val), "charset=") {
					attribute.Val = "text/html; charset=utf-8"
				}
			}
		}
		rewritten.WriteString(token.String())
	}
	return []byte(crawler.RewriteDocumentResourceReferences(rewritten.String(), resolve)), nil
}
