package crawler

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// ReferenceContext tells parser rules where a reference was found.
// Browsers do not resolve every string literal the same way: HTML attributes,
// CSS url(...) values, and JavaScript module manifests each have their own
// conventions. Keeping the context explicit makes new crawler quirks local to
// this package instead of spreading conditional URL handling through the app.
type ReferenceContext int

const (
	ReferenceDocument ReferenceContext = iota
	ReferenceJavaScript
)

// Parser rewrites references found in external pages.
//
// The package owns parsing rules. The application owns side effects: fetching,
// quota decisions, asset persistence, whole-site page routing, and progress.
// Add new site-specific or framework-specific parsing rules here by extending
// the relevant Rewrite* method or adding a narrow helper next to the rule it
// supports.
type Parser struct {
	NormalizeURL                         func(rawRef string, baseURL *url.URL, referenceContext ReferenceContext) (string, bool)
	RewriteResourceReference             func(rawRef string, baseURL *url.URL, depth int, referenceContext ReferenceContext) string
	RewriteDocumentResourceReference     func(rawRef string, baseURL *url.URL, depth int) string
	DocumentURLRewriter                  func(normalizedURL string) (string, bool)
	ShouldBlankEmbeddedDocumentReference func(tagName, normalizedURL string) bool
	ShouldRewriteImageAltResource        func(rawRef string, baseURL *url.URL) bool
}

var (
	htmlResourcePattern = regexp.MustCompile(`(?is)<(a|area|link|script|img|source|video|audio|iframe|embed|object|form)\b[^>]*(href|xlink:href|src|poster|data|action)\s*=\s*["']([^"']+)["']`)
	htmlImageAltPattern = regexp.MustCompile(`(?is)<img\b[^>]*\balt\s*=\s*["']([^"']+)["'][^>]*>`)
	htmlSrcSetPattern   = regexp.MustCompile(`(?is)\bsrcset\s*=\s*["']([^"']+)["']`)
	cssURLPattern       = regexp.MustCompile(`(?is)url\(\s*['"]?([^'")]+)['"]?\s*\)`)
	cssImportPattern    = regexp.MustCompile(`(?is)@import\s+(?:url\(\s*)?['"]?([^'")\s;]+)['"]?`)
	staticURLPattern    = regexp.MustCompile(`(?is)https?://[^\s"'<>\\)]+`)
	linkManifestPattern = regexp.MustCompile(`(?is)\brel\s*=\s*["'][^"']*\bmanifest\b[^"']*["']`)
)

func (parser Parser) RewriteTextReferences(source, baseRawURL string, depth int) string {
	baseURL, _ := url.Parse(baseRawURL)
	rewriteSingle := func(rawRef string) string {
		return parser.rewriteResource(rawRef, baseURL, depth, ReferenceDocument)
	}
	rewriteDocumentReference := func(rawRef string) string {
		if parser.RewriteDocumentResourceReference == nil {
			return rewriteSingle(rawRef)
		}
		return parser.RewriteDocumentResourceReference(rawRef, baseURL, depth)
	}
	rewritten := htmlResourcePattern.ReplaceAllStringFunc(source, func(match string) string {
		parts := htmlResourcePattern.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match
		}
		tagName := strings.ToLower(strings.TrimSpace(parts[1]))
		attributeName := strings.ToLower(strings.TrimSpace(parts[2]))
		if tagName == "link" && attributeName == "href" && isManifestLinkTag(match) {
			manifestReference := parser.rewriteDataManifestReference(parts[3], baseURL)
			if manifestReference != parts[3] {
				return strings.Replace(match, parts[3], manifestReference, 1)
			}
		}
		normalizedURL, blocked := parser.normalize(parts[3], baseURL, ReferenceDocument)
		if !blocked && parser.DocumentURLRewriter != nil && isWholeSiteDocumentAttribute(tagName, attributeName) && IsWholeSitePageURLString(normalizedURL) {
			if rewrittenURL, ok := parser.DocumentURLRewriter(normalizedURL); ok {
				return strings.Replace(match, parts[3], rewrittenURL, 1)
			}
			return match
		}
		if !blocked && parser.ShouldBlankEmbeddedDocumentReference != nil && parser.ShouldBlankEmbeddedDocumentReference(tagName, normalizedURL) {
			return strings.Replace(match, parts[3], "about:blank", 1)
		}
		if isWholeSiteDocumentAttribute(tagName, attributeName) {
			return strings.Replace(match, parts[3], rewriteDocumentReference(parts[3]), 1)
		}
		return strings.Replace(match, parts[3], rewriteSingle(parts[3]), 1)
	})
	rewritten = htmlImageAltPattern.ReplaceAllStringFunc(rewritten, func(match string) string {
		parts := htmlImageAltPattern.FindStringSubmatch(match)
		if len(parts) != 2 || parser.ShouldRewriteImageAltResource == nil || !parser.ShouldRewriteImageAltResource(parts[1], baseURL) {
			return match
		}
		return strings.Replace(match, parts[1], rewriteSingle(parts[1]), 1)
	})
	rewritten = RewriteSrcSetReferences(rewritten, rewriteSingle)
	rewritten = RewriteCSSImportReferences(rewritten, rewriteSingle)
	rewritten = RewriteCSSURLReferences(rewritten, rewriteSingle)
	rewritten = parser.RewriteStaticURLTextReferences(rewritten, baseURL, depth)
	return rewritten
}

func (parser Parser) RewriteJavaScriptReferences(source, baseRawURL string, depth int) string {
	baseURL, _ := url.Parse(baseRawURL)
	var rewritten strings.Builder
	lastWrittenIndex := 0
	for currentIndex := 0; currentIndex < len(source); currentIndex++ {
		quote := source[currentIndex]
		if quote != '\'' && quote != '"' && quote != '`' {
			continue
		}
		referenceStart := currentIndex + 1
		referenceEnd := referenceStart
		escaped := false
		for referenceEnd < len(source) {
			currentByte := source[referenceEnd]
			if escaped {
				escaped = false
				referenceEnd++
				continue
			}
			if currentByte == '\\' {
				escaped = true
				referenceEnd++
				continue
			}
			if currentByte == quote {
				break
			}
			referenceEnd++
		}
		if referenceEnd >= len(source) || source[referenceEnd] != quote {
			break
		}
		rawReference := source[referenceStart:referenceEnd]
		if !ShouldRewriteJSResourceReference(rawReference) {
			currentIndex = referenceEnd
			continue
		}
		normalizedURL, blocked := parser.normalize(rawReference, baseURL, ReferenceJavaScript)
		if !blocked && HasAllowedResourceExtension(normalizedURL) {
			rewritten.WriteString(source[lastWrittenIndex:referenceStart])
			rewritten.WriteString(parser.rewriteResource(rawReference, baseURL, depth, ReferenceJavaScript))
			lastWrittenIndex = referenceEnd
		}
		currentIndex = referenceEnd
	}
	if lastWrittenIndex == 0 {
		return source
	}
	rewritten.WriteString(source[lastWrittenIndex:])
	return rewritten.String()
}

func (parser Parser) RewriteStaticURLTextReferences(source string, baseURL *url.URL, depth int) string {
	return staticURLPattern.ReplaceAllStringFunc(source, func(rawURL string) string {
		resourceURL, trailingText := SplitStaticResourceURLTrailingText(rawURL)
		if !HasAllowedResourceExtension(resourceURL) {
			return rawURL
		}
		normalizedURL, blocked := parser.normalize(resourceURL, baseURL, ReferenceDocument)
		if blocked || normalizedURL == "" || !HasAllowedResourceExtension(normalizedURL) {
			return rawURL
		}
		rewrittenURL := parser.rewriteResource(resourceURL, baseURL, depth, ReferenceDocument)
		if rewrittenURL == "" || strings.HasPrefix(rewrittenURL, "http://") || strings.HasPrefix(rewrittenURL, "https://") {
			return rawURL
		}
		return rewrittenURL + trailingText
	})
}

func RewriteSrcSetReferences(source string, rewriteSingle func(string) string) string {
	return htmlSrcSetPattern.ReplaceAllStringFunc(source, func(match string) string {
		parts := htmlSrcSetPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		candidates := strings.Split(parts[1], ",")
		for index, candidate := range candidates {
			fields := strings.Fields(strings.TrimSpace(candidate))
			if len(fields) == 0 {
				continue
			}
			fields[0] = rewriteSingle(fields[0])
			candidates[index] = strings.Join(fields, " ")
		}
		return strings.Replace(match, parts[1], strings.Join(candidates, ", "), 1)
	})
}

func RewriteCSSImportReferences(source string, rewriteSingle func(string) string) string {
	return cssImportPattern.ReplaceAllStringFunc(source, func(match string) string {
		parts := cssImportPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		return strings.Replace(match, parts[1], rewriteSingle(parts[1]), 1)
	})
}

func RewriteDocumentResourceReferences(source string, rewriteSingle func(string) string) string {
	rewritten := htmlResourcePattern.ReplaceAllStringFunc(source, func(match string) string {
		parts := htmlResourcePattern.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match
		}
		return strings.Replace(match, parts[3], rewriteSingle(parts[3]), 1)
	})
	rewritten = RewriteSrcSetReferences(rewritten, rewriteSingle)
	rewritten = RewriteCSSImportReferences(rewritten, rewriteSingle)
	rewritten = RewriteCSSURLReferences(rewritten, rewriteSingle)
	return rewritten
}

func RewriteCSSURLReferences(source string, rewriteSingle func(string) string) string {
	matches := cssURLPattern.FindAllStringSubmatchIndex(source, -1)
	if len(matches) == 0 {
		return source
	}
	var rewritten strings.Builder
	lastEnd := 0
	for _, match := range matches {
		if len(match) != 4 {
			continue
		}
		matchStart := match[0]
		matchEnd := match[1]
		referenceStart := match[2]
		referenceEnd := match[3]
		rewritten.WriteString(source[lastEnd:matchStart])
		if isCSSImportURL(source, matchStart) {
			rewritten.WriteString(source[matchStart:matchEnd])
		} else {
			rewritten.WriteString(source[matchStart:referenceStart])
			rewritten.WriteString(rewriteSingle(source[referenceStart:referenceEnd]))
			rewritten.WriteString(source[referenceEnd:matchEnd])
		}
		lastEnd = matchEnd
	}
	rewritten.WriteString(source[lastEnd:])
	return rewritten.String()
}

func (parser Parser) rewriteResource(rawRef string, baseURL *url.URL, depth int, referenceContext ReferenceContext) string {
	if parser.RewriteResourceReference == nil {
		return rawRef
	}
	return parser.RewriteResourceReference(rawRef, baseURL, depth, referenceContext)
}

func (parser Parser) normalize(rawRef string, baseURL *url.URL, referenceContext ReferenceContext) (string, bool) {
	if parser.NormalizeURL == nil {
		return NormalizeURL(rawRef, baseURL, referenceContext)
	}
	return parser.NormalizeURL(rawRef, baseURL, referenceContext)
}

func SplitStaticResourceURLTrailingText(rawURL string) (string, string) {
	resourceURL := strings.TrimSpace(rawURL)
	trailingLength := 0
	for len(resourceURL) > 0 {
		lastByte := resourceURL[len(resourceURL)-1]
		if !strings.ContainsRune(".,;:", rune(lastByte)) {
			break
		}
		resourceURL = resourceURL[:len(resourceURL)-1]
		trailingLength++
	}
	if trailingLength == 0 {
		return resourceURL, ""
	}
	return resourceURL, rawURL[len(rawURL)-trailingLength:]
}

func NormalizeURL(rawRef string, baseURL *url.URL, referenceContext ReferenceContext) (string, bool) {
	trimmedRef := strings.TrimSpace(rawRef)
	if trimmedRef == "" || strings.HasPrefix(trimmedRef, "#") {
		return "", true
	}
	if IsSuspiciousReference(trimmedRef) {
		return "", true
	}
	loweredRef := strings.ToLower(trimmedRef)
	for _, blockedPrefix := range []string{"mailto:", "tel:", "javascript:", "data:", "blob:"} {
		if strings.HasPrefix(loweredRef, blockedPrefix) {
			return "", true
		}
	}
	parsedRef, err := url.Parse(trimmedRef)
	if err != nil {
		return "", true
	}
	resolutionBaseURL := baseURL
	if referenceContext == ReferenceJavaScript && shouldResolveJavaScriptReferenceAgainstOriginRoot(trimmedRef, baseURL) {
		resolutionBaseURL = OriginRootURL(baseURL)
	}
	resolved := resolutionBaseURL.ResolveReference(parsedRef)
	if resolved == nil || resolved.Scheme == "" {
		return "", true
	}
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", true
	}
	resolved.Fragment = ""
	resolved.ForceQuery = false
	return resolved.String(), false
}

func IsSuspiciousReference(rawRef string) bool {
	loweredRef := strings.ToLower(strings.TrimSpace(rawRef))
	if loweredRef == "" {
		return true
	}
	if strings.Contains(loweredRef, "${") || strings.ContainsAny(loweredRef, "+()[],") {
		return true
	}
	for _, blockedFragment := range []string{"this.", ".src", ".url", "params", "videoid", "void"} {
		if strings.Contains(loweredRef, blockedFragment) {
			return true
		}
	}
	return false
}

func ShouldRewriteJSResourceReference(rawReference string) bool {
	trimmedReference := strings.TrimSpace(rawReference)
	if trimmedReference == "" {
		return false
	}
	for _, rule := range []func(string) bool{
		isExplicitJSResourceReference,
		isRootRelativeJSResourceReference,
		isDotRelativeJSResourceReference,
		isBareStaticFileJSResourceReference,
	} {
		if rule(trimmedReference) {
			return true
		}
	}
	return false
}

func ResourceExtension(rawRef string) string {
	parsedRef, err := url.Parse(strings.TrimSpace(rawRef))
	if err == nil && parsedRef.Path != "" {
		return strings.ToLower(path.Ext(parsedRef.Path))
	}
	withoutFragment := strings.SplitN(rawRef, "#", 2)[0]
	withoutQuery := strings.SplitN(withoutFragment, "?", 2)[0]
	return strings.ToLower(path.Ext(withoutQuery))
}

func HasAllowedResourceExtension(resourceURL string) bool {
	_, found := KnownResourceKindsByExtension[ResourceExtension(resourceURL)]
	return found
}

func ResourceKindFromURL(resourceURL string) string {
	extension := ResourceExtension(resourceURL)
	if resourceKind, found := KnownResourceKindsByExtension[extension]; found {
		return resourceKind
	}
	return ""
}

var KnownResourceKindsByExtension = map[string]string{
	".css":         "style",
	".js":          "script",
	".mjs":         "script",
	".cjs":         "script",
	".png":         "image",
	".jpg":         "image",
	".jpeg":        "image",
	".gif":         "image",
	".svg":         "image",
	".webp":        "image",
	".ico":         "image",
	".bmp":         "image",
	".tif":         "image",
	".tiff":        "image",
	".avif":        "image",
	".apng":        "image",
	".heic":        "image",
	".heif":        "image",
	".jfif":        "image",
	".pjpeg":       "image",
	".pjp":         "image",
	".woff":        "font",
	".woff2":       "font",
	".ttf":         "font",
	".eot":         "font",
	".otf":         "font",
	".mp4":         "video",
	".webm":        "video",
	".mov":         "video",
	".avi":         "video",
	".mkv":         "video",
	".m4v":         "video",
	".flv":         "video",
	".wmv":         "video",
	".mpg":         "video",
	".mpeg":        "video",
	".3gp":         "video",
	".3g2":         "video",
	".ts":          "video",
	".m2ts":        "video",
	".mts":         "video",
	".ogv":         "video",
	".m3u8":        "video",
	".mp3":         "audio",
	".ogg":         "audio",
	".oga":         "audio",
	".opus":        "audio",
	".wav":         "audio",
	".flac":        "audio",
	".aac":         "audio",
	".m4a":         "audio",
	".wma":         "audio",
	".aiff":        "audio",
	".mid":         "audio",
	".midi":        "audio",
	".amr":         "audio",
	".weba":        "audio",
	".pdf":         "file",
	".doc":         "file",
	".docx":        "file",
	".dot":         "file",
	".dotx":        "file",
	".xls":         "file",
	".xlsx":        "file",
	".xlsm":        "file",
	".csv":         "file",
	".tsv":         "file",
	".ods":         "file",
	".odt":         "file",
	".odp":         "file",
	".odg":         "file",
	".odf":         "file",
	".ppt":         "file",
	".pptx":        "file",
	".pps":         "file",
	".ppsx":        "file",
	".pot":         "file",
	".potx":        "file",
	".rtf":         "file",
	".txt":         "file",
	".text":        "file",
	".md":          "file",
	".markdown":    "file",
	".epub":        "file",
	".mobi":        "file",
	".azw":         "file",
	".azw3":        "file",
	".fb2":         "file",
	".djvu":        "file",
	".djv":         "file",
	".cbz":         "file",
	".cbr":         "file",
	".xml":         "file",
	".json":        "file",
	".map":         "file",
	".geojson":     "file",
	".yaml":        "file",
	".yml":         "file",
	".toml":        "file",
	".ini":         "file",
	".cfg":         "file",
	".conf":        "file",
	".log":         "file",
	".sql":         "file",
	".db":          "file",
	".sqlite":      "file",
	".sqlite3":     "file",
	".zip":         "file",
	".rar":         "file",
	".7z":          "file",
	".tar":         "file",
	".gz":          "file",
	".tgz":         "file",
	".bz2":         "file",
	".xz":          "file",
	".lz":          "file",
	".lzma":        "file",
	".zst":         "file",
	".cab":         "file",
	".jar":         "file",
	".war":         "file",
	".ear":         "file",
	".apk":         "file",
	".ipa":         "file",
	".exe":         "file",
	".msi":         "file",
	".msix":        "file",
	".dmg":         "file",
	".pkg":         "file",
	".deb":         "file",
	".rpm":         "file",
	".appimage":    "file",
	".bin":         "file",
	".iso":         "file",
	".img":         "file",
	".toast":       "file",
	".kmz":         "file",
	".kml":         "file",
	".gpx":         "file",
	".rctrk":       "file",
	".torrent":     "file",
	".webmanifest": "file",
}

func isWholeSiteDocumentAttribute(tagName, attributeName string) bool {
	switch strings.ToLower(strings.TrimSpace(tagName)) {
	case "a", "area":
		return attributeName == "href" || attributeName == "xlink:href"
	case "form":
		return attributeName == "action"
	case "iframe", "embed":
		return attributeName == "src"
	case "object":
		return attributeName == "data"
	default:
		return false
	}
}

func IsWholeSitePageURLString(rawURL string) bool {
	parsedURL, err := url.Parse(rawURL)
	return err == nil && IsWholeSitePageURL(parsedURL)
}

func IsWholeSitePageURL(pageURL *url.URL) bool {
	if pageURL == nil {
		return false
	}
	extension := strings.ToLower(path.Ext(pageURL.Path))
	switch extension {
	case "", ".htm", ".html", ".xhtml", ".php", ".asp", ".aspx", ".jsp", ".cgi":
		return true
	default:
		return false
	}
}

func isExplicitJSResourceReference(rawReference string) bool {
	loweredReference := strings.ToLower(strings.TrimSpace(rawReference))
	return strings.HasPrefix(loweredReference, "http://") || strings.HasPrefix(loweredReference, "https://") || strings.HasPrefix(loweredReference, "//")
}

func isRootRelativeJSResourceReference(rawReference string) bool {
	trimmedReference := strings.TrimSpace(rawReference)
	return strings.HasPrefix(trimmedReference, "/") && isStaticLikeJSReference(trimmedReference)
}

func isDotRelativeJSResourceReference(rawReference string) bool {
	trimmedReference := strings.TrimSpace(rawReference)
	if strings.HasPrefix(trimmedReference, "./") || strings.HasPrefix(trimmedReference, "../") {
		return isStaticLikeJSReference(trimmedReference)
	}
	return false
}

func isBareStaticFileJSResourceReference(rawReference string) bool {
	trimmedReference := strings.TrimSpace(rawReference)
	if strings.Contains(trimmedReference, "://") || strings.HasPrefix(trimmedReference, "//") || strings.HasPrefix(trimmedReference, "/") || strings.HasPrefix(trimmedReference, "./") || strings.HasPrefix(trimmedReference, "../") {
		return false
	}
	return isStaticLikeJSReference(trimmedReference)
}

func isStaticLikeJSReference(rawReference string) bool {
	trimmedReference := strings.TrimSpace(rawReference)
	if trimmedReference == "" {
		return false
	}
	if strings.ContainsAny(trimmedReference, ` "'`+"`"+`*<>{}()[]|^$`) {
		return false
	}
	if strings.HasPrefix(trimmedReference, ".") && !strings.HasPrefix(trimmedReference, "./") && !strings.HasPrefix(trimmedReference, "../") {
		return false
	}
	return HasAllowedResourceExtension(trimmedReference)
}

func shouldResolveJavaScriptReferenceAgainstOriginRoot(rawRef string, baseURL *url.URL) bool {
	trimmedRef := strings.TrimSpace(rawRef)
	if trimmedRef == "" || baseURL == nil {
		return false
	}
	if strings.Contains(trimmedRef, "://") || strings.HasPrefix(trimmedRef, "//") || strings.HasPrefix(trimmedRef, "/") || strings.HasPrefix(trimmedRef, "./") || strings.HasPrefix(trimmedRef, "../") {
		return false
	}
	rawFirstSegment := FirstPathSegment(trimmedRef)
	baseFirstSegment := FirstPathSegment(strings.TrimPrefix(baseURL.Path, "/"))
	return rawFirstSegment != "" && strings.EqualFold(rawFirstSegment, baseFirstSegment)
}

func OriginRootURL(baseURL *url.URL) *url.URL {
	if baseURL == nil {
		return baseURL
	}
	rootURL := *baseURL
	rootURL.Path = "/"
	rootURL.RawPath = ""
	rootURL.RawQuery = ""
	rootURL.ForceQuery = false
	rootURL.Fragment = ""
	return &rootURL
}

func FirstPathSegment(rawPath string) string {
	trimmedPath := strings.TrimLeft(strings.TrimSpace(rawPath), "/")
	if trimmedPath == "" {
		return ""
	}
	segmentEnd := strings.IndexByte(trimmedPath, '/')
	if segmentEnd < 0 {
		return trimmedPath
	}
	return trimmedPath[:segmentEnd]
}

func isCSSImportURL(source string, urlStart int) bool {
	prefixStart := strings.LastIndexAny(source[:urlStart], ";{}>")
	statementPrefix := strings.ToLower(strings.TrimSpace(source[prefixStart+1 : urlStart]))
	return strings.HasPrefix(statementPrefix, "@import")
}

func isManifestLinkTag(rawTag string) bool {
	return linkManifestPattern.MatchString(rawTag)
}

func (parser Parser) rewriteDataManifestReference(rawRef string, baseURL *url.URL) string {
	header, payload, ok := SplitDataURL(rawRef)
	if !ok || !isManifestDataURLHeader(header) {
		return rawRef
	}
	manifestBytes, decodeErr := decodeDataURLPayload(header, payload)
	if decodeErr != nil {
		return rawRef
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return rawRef
	}
	changed := false
	for _, fieldName := range []string{"start_url", "scope", "id"} {
		if removeManifestURLStringField(manifest, fieldName) {
			changed = true
		}
	}
	for _, fieldName := range []string{"icons", "screenshots"} {
		if rewriteManifestURLObjectArrayField(manifest, fieldName, "src", parser, baseURL) {
			changed = true
		}
	}
	if rewriteManifestURLObjectArrayField(manifest, "related_applications", "url", parser, baseURL) {
		changed = true
	}
	if rewriteManifestShortcuts(manifest, parser, baseURL) {
		changed = true
	}
	if removeManifestShareTarget(manifest) {
		changed = true
	}
	if !changed {
		return rawRef
	}
	rewrittenBytes, marshalErr := json.Marshal(manifest)
	if marshalErr != nil {
		return rawRef
	}
	return manifestDataURLHeader(header) + "," + url.PathEscape(string(rewrittenBytes))
}

func SplitDataURL(rawRef string) (string, string, bool) {
	trimmedRef := strings.TrimSpace(rawRef)
	if !strings.HasPrefix(strings.ToLower(trimmedRef), "data:") {
		return "", "", false
	}
	commaIndex := strings.Index(trimmedRef, ",")
	if commaIndex < 0 {
		return "", "", false
	}
	return trimmedRef[:commaIndex], trimmedRef[commaIndex+1:], true
}

func isManifestDataURLHeader(header string) bool {
	loweredHeader := strings.ToLower(strings.TrimSpace(header))
	return strings.Contains(loweredHeader, "manifest+json") || strings.Contains(loweredHeader, "application/json")
}

func decodeDataURLPayload(header, payload string) ([]byte, error) {
	if strings.Contains(strings.ToLower(header), ";base64") {
		decodedBytes, err := base64.StdEncoding.DecodeString(payload)
		if err == nil {
			return decodedBytes, nil
		}
		return base64.RawStdEncoding.DecodeString(payload)
	}
	decodedPayload, err := url.PathUnescape(payload)
	return []byte(decodedPayload), err
}

func manifestDataURLHeader(header string) string {
	cleanHeader := strings.TrimSpace(header)
	if cleanHeader == "" || strings.EqualFold(cleanHeader, "data:") {
		return "data:application/manifest+json"
	}
	headerParts := strings.Split(cleanHeader, ";")
	filteredParts := headerParts[:0]
	for _, headerPart := range headerParts {
		if strings.EqualFold(strings.TrimSpace(headerPart), "base64") {
			continue
		}
		filteredParts = append(filteredParts, headerPart)
	}
	return strings.Join(filteredParts, ";")
}

func removeManifestURLStringField(manifest map[string]any, fieldName string) bool {
	if _, ok := manifest[fieldName].(string); !ok {
		return false
	}
	// Data URL manifests do not have the imported page as their own URL. Keeping
	// navigation fields from the source site makes browsers compare the source
	// origin with the local copy and reject the manifest. Let the browser use its
	// defaults instead of pinning imported pages to the source origin.
	delete(manifest, fieldName)
	return true
}

func rewriteManifestURLStringField(manifest map[string]any, fieldName string, parser Parser, baseURL *url.URL) bool {
	rawValue, ok := manifest[fieldName].(string)
	if !ok {
		return false
	}
	rewrittenValue := rewriteManifestURLString(rawValue, parser, baseURL)
	if rewrittenValue == rawValue {
		return false
	}
	manifest[fieldName] = rewrittenValue
	return true
}

func rewriteManifestURLObjectArrayField(manifest map[string]any, arrayFieldName, urlFieldName string, parser Parser, baseURL *url.URL) bool {
	rawEntries, ok := manifest[arrayFieldName].([]any)
	if !ok {
		return false
	}
	changed := false
	for _, rawEntry := range rawEntries {
		entryObject, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		if rewriteManifestURLStringField(entryObject, urlFieldName, parser, baseURL) {
			changed = true
		}
	}
	return changed
}

func rewriteManifestShortcuts(manifest map[string]any, parser Parser, baseURL *url.URL) bool {
	rawShortcuts, ok := manifest["shortcuts"].([]any)
	if !ok {
		return false
	}
	changed := false
	for _, rawShortcut := range rawShortcuts {
		shortcutObject, ok := rawShortcut.(map[string]any)
		if !ok {
			continue
		}
		if removeManifestURLStringField(shortcutObject, "url") {
			changed = true
		}
		if rewriteManifestURLObjectArrayField(shortcutObject, "icons", "src", parser, baseURL) {
			changed = true
		}
	}
	return changed
}

func removeManifestShareTarget(manifest map[string]any) bool {
	if _, ok := manifest["share_target"].(map[string]any); !ok {
		return false
	}
	delete(manifest, "share_target")
	return true
}

func rewriteManifestURLString(rawValue string, parser Parser, baseURL *url.URL) string {
	normalizedURL, blocked := parser.normalize(rawValue, baseURL, ReferenceDocument)
	if blocked || normalizedURL == "" {
		return rawValue
	}
	return normalizedURL
}
