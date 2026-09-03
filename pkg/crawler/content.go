package crawler

import (
	"bytes"
	"encoding/xml"
	"mime"
	"net/http"
	"sort"
	"strings"
)

func NormalizedResourceContentType(contentTypeHeader string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(contentTypeHeader, ";")[0]))
}

func ResourceKindFromContentType(contentType string) string {
	switch {
	case contentType == "text/css":
		return "style"
	case strings.Contains(contentType, "javascript"), strings.Contains(contentType, "ecmascript"):
		return "script"
	case strings.HasPrefix(contentType, "image/"):
		return "image"
	case strings.HasPrefix(contentType, "font/"), strings.Contains(contentType, "font"):
		return "font"
	case strings.HasPrefix(contentType, "video/"):
		return "video"
	case strings.HasPrefix(contentType, "audio/"):
		return "audio"
	case strings.HasPrefix(contentType, "text/"), strings.HasPrefix(contentType, "application/"):
		return "file"
	default:
		return ""
	}
}

func EffectiveResourceContentType(resourceURL, contentType string) string {
	contentType = NormalizedResourceContentType(contentType)
	resourceKind := ResourceKindFromURL(resourceURL)
	if resourceKind == "" || !isLegacyLooseTextResourceContentType(contentType) {
		return contentType
	}
	switch resourceKind {
	case "script":
		return "application/javascript"
	case "style":
		return "text/css"
	default:
		return contentType
	}
}

func DetectedResourceContentType(resourceURL, declaredContentType string, contentSample []byte) string {
	declaredContentType = EffectiveResourceContentType(resourceURL, declaredContentType)
	if len(contentSample) == 0 {
		return declaredContentType
	}

	detectedContentType := NormalizedResourceContentType(http.DetectContentType(contentSample))
	if detectedContentType == "text/html" || detectedContentType == "application/xhtml+xml" {
		return detectedContentType
	}
	if isSVGContent(contentSample) {
		return "image/svg+xml"
	}
	if (declaredContentType == "image/svg+xml" || declaredContentType == "application/xhtml+xml") && isGenericXMLContentType(detectedContentType) {
		return declaredContentType
	}
	if detectedContentType != "text/plain" && detectedContentType != "application/octet-stream" {
		return detectedContentType
	}
	if declaredContentType != "" {
		return declaredContentType
	}
	return detectedContentType
}

func isSVGContent(contentSample []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(contentSample))
	decoder.Strict = false
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		startElement, found := token.(xml.StartElement)
		if !found {
			continue
		}
		return strings.EqualFold(strings.TrimSpace(startElement.Name.Local), "svg")
	}
}

func isGenericXMLContentType(contentType string) bool {
	switch contentType {
	case "text/xml", "application/xml", "text/plain", "application/octet-stream":
		return true
	default:
		return false
	}
}

func ErrorReason(err error) string {
	if err == nil {
		return "error"
	}
	errorText := strings.TrimSpace(err.Error())
	for _, token := range strings.Fields(errorText) {
		code := strings.Trim(token, ".,:;()[]{}\"'")
		if len(code) == 3 && code[0] >= '1' && code[0] <= '5' && code[1] >= '0' && code[1] <= '9' && code[2] >= '0' && code[2] <= '9' {
			return code
		}
	}
	loweredText := strings.ToLower(errorText)
	switch {
	case strings.Contains(loweredText, "timeout") || strings.Contains(loweredText, "deadline"):
		return "timeout"
	case strings.Contains(loweredText, "no such host") || strings.Contains(loweredText, "dns") || strings.Contains(loweredText, "resolve"):
		return "dns"
	case strings.Contains(loweredText, "tls") || strings.Contains(loweredText, "certificate") || strings.Contains(loweredText, "handshake"):
		return "tls"
	case strings.Contains(loweredText, "refused"):
		return "refused"
	case strings.Contains(loweredText, "read"):
		return "read"
	default:
		return "network"
	}
}

func ResourceExtensionFromContentType(contentType string) string {
	switch contentType {
	case "text/css":
		return ".css"
	case "application/javascript", "text/javascript", "application/x-javascript":
		return ".js"
	case "application/ecmascript", "text/ecmascript":
		return ".mjs"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/svg+xml":
		return ".svg"
	case "image/webp":
		return ".webp"
	case "image/x-icon", "image/vnd.microsoft.icon":
		return ".ico"
	case "font/woff":
		return ".woff"
	case "font/woff2":
		return ".woff2"
	case "font/ttf", "application/x-font-ttf":
		return ".ttf"
	case "font/otf", "application/x-font-opentype":
		return ".otf"
	case "application/vnd.ms-fontobject":
		return ".eot"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav", "audio/wave", "audio/x-wav":
		return ".wav"
	}
	extensions, err := mime.ExtensionsByType(contentType)
	if err != nil || len(extensions) == 0 {
		return ""
	}
	sort.Strings(extensions)
	return strings.ToLower(strings.TrimSpace(extensions[0]))
}

func DownloadedResourceExtension(resourceURL, contentType string) string {
	resourceExtension := ResourceExtension(resourceURL)
	normalizedContentType := NormalizedResourceContentType(contentType)

	// Generic response types do not describe the downloaded format. In that
	// case the source URL remains the best available naming hint.
	switch normalizedContentType {
	case "", "application/octet-stream", "binary/octet-stream", "text/plain":
		return resourceExtension
	case "application/zip":
		if isZIPContainerExtension(resourceExtension) {
			return resourceExtension
		}
	}

	contentExtension := ResourceExtensionFromContentType(normalizedContentType)
	if contentExtension != "" {
		return contentExtension
	}
	return resourceExtension
}

func isZIPContainerExtension(extension string) bool {
	switch strings.ToLower(strings.TrimSpace(extension)) {
	case ".docx", ".dotx", ".xlsx", ".xlsm", ".pptx", ".ppsx", ".potx", ".epub", ".jar", ".war", ".ear", ".apk":
		return true
	default:
		return false
	}
}

func NormalizeMirroredAssetReference(assetPath string) string {
	trimmedPath := strings.TrimSpace(assetPath)
	if trimmedPath == "" {
		return ""
	}
	if strings.HasPrefix(trimmedPath, "http://") || strings.HasPrefix(trimmedPath, "https://") || strings.HasPrefix(trimmedPath, "data:") || strings.HasPrefix(trimmedPath, "blob:") {
		return trimmedPath
	}
	if strings.HasPrefix(trimmedPath, "//") {
		return "/" + strings.TrimLeft(trimmedPath, "/")
	}
	return trimmedPath
}

func isLegacyLooseTextResourceContentType(contentType string) bool {
	switch contentType {
	case "", "text/html", "application/xhtml+xml", "text/plain", "application/octet-stream":
		return true
	default:
		return false
	}
}
