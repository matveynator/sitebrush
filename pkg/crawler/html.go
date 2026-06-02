package crawler

import (
	"bytes"
	"io"
	"mime"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
)

type DecodeResult struct {
	Text     string
	Encoding string
}

type htmlEncodingCandidate struct {
	label     string
	encoding  encoding.Encoding
	preferred bool
}

type scoredTextEncoding struct {
	label string
	text  string
	score int
	order int
}

var htmlFallbackEncodingLabels = []string{
	"utf-8",
	"windows-1251", "koi8-r", "koi8-u", "iso-8859-5", "ibm866", "x-mac-cyrillic",
	"windows-1252", "iso-8859-1", "windows-1250", "iso-8859-2", "windows-1254", "iso-8859-9",
	"windows-1257", "iso-8859-13", "windows-1253", "iso-8859-7", "windows-1255", "iso-8859-8",
	"windows-1256", "iso-8859-6", "windows-1258",
	"gb18030", "big5", "shift_jis", "euc-jp", "iso-2022-jp", "euc-kr",
}

func DecodeHTML(htmlBytes []byte, contentType string) DecodeResult {
	return DecodeText(htmlBytes, contentType)
}

func DecodeText(textBytes []byte, contentType string) DecodeResult {
	candidates := htmlEncodingCandidates(textBytes, contentType)
	scoredCandidates := make([]scoredTextEncoding, 0, len(candidates))
	for candidateIndex, candidate := range candidates {
		decodedText, ok := decodeWithEncoding(textBytes, candidate.label, candidate.encoding)
		if !ok {
			continue
		}
		score := scoreDecodedHTMLText(decodedText)
		if candidate.preferred {
			score += 250
		}
		if candidate.label == "utf-8" {
			score += 600
		}
		scoredCandidates = append(scoredCandidates, scoredTextEncoding{label: candidate.label, text: decodedText, score: score, order: candidateIndex})
	}
	if len(scoredCandidates) == 0 {
		return DecodeResult{Text: string(textBytes), Encoding: "unknown"}
	}
	sort.SliceStable(scoredCandidates, func(leftIndex, rightIndex int) bool {
		leftCandidate := scoredCandidates[leftIndex]
		rightCandidate := scoredCandidates[rightIndex]
		if leftCandidate.score != rightCandidate.score {
			return leftCandidate.score > rightCandidate.score
		}
		return leftCandidate.order < rightCandidate.order
	})
	bestCandidate := scoredCandidates[0]
	return DecodeResult{Text: bestCandidate.text, Encoding: bestCandidate.label}
}

func ExtractPageLinks(htmlSource string, baseURL, siteURL *url.URL) []*url.URL {
	pageURLs := make([]*url.URL, 0, 16)
	tokenizer := html.NewTokenizer(strings.NewReader(htmlSource))
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}
		if tokenType != html.StartTagToken && tokenType != html.SelfClosingTagToken {
			continue
		}
		token := tokenizer.Token()
		tagName := strings.ToLower(strings.TrimSpace(token.Data))
		for _, attribute := range token.Attr {
			attributeName := strings.ToLower(strings.TrimSpace(attribute.Key))
			if !isDocumentAttribute(tagName, attributeName) {
				continue
			}
			normalizedURL, blocked := NormalizeURL(attribute.Val, baseURL, ReferenceDocument)
			if blocked || normalizedURL == "" {
				continue
			}
			linkedPageURL, parseErr := url.Parse(normalizedURL)
			if parseErr != nil || !SameHost(siteURL, linkedPageURL) || !IsPageURL(linkedPageURL) {
				continue
			}
			pageURLs = append(pageURLs, linkedPageURL)
		}
	}
	return pageURLs
}

func SameHost(leftURL, rightURL *url.URL) bool {
	if leftURL == nil || rightURL == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(leftURL.Host), strings.TrimSpace(rightURL.Host))
}

func IsPageURL(pageURL *url.URL) bool {
	if pageURL == nil {
		return false
	}
	extension := strings.ToLower(pathExt(pageURL.Path))
	if extension == "" {
		return true
	}
	switch extension {
	case ".htm", ".html", ".xhtml", ".php", ".asp", ".aspx", ".jsp", ".cgi":
		return true
	default:
		return false
	}
}

func htmlEncodingCandidates(htmlBytes []byte, contentType string) []htmlEncodingCandidate {
	candidates := make([]htmlEncodingCandidate, 0, len(htmlFallbackEncodingLabels)+3)
	addedLabels := make(map[string]struct{})
	addCandidate := func(label string, preferred bool) {
		normalizedLabel := strings.ToLower(strings.TrimSpace(label))
		if normalizedLabel == "" {
			return
		}
		candidateEncoding, canonicalName := charset.Lookup(normalizedLabel)
		if candidateEncoding == nil {
			return
		}
		canonicalName = strings.ToLower(strings.TrimSpace(canonicalName))
		if canonicalName == "" {
			canonicalName = normalizedLabel
		}
		if _, exists := addedLabels[canonicalName]; exists {
			return
		}
		addedLabels[canonicalName] = struct{}{}
		candidates = append(candidates, htmlEncodingCandidate{label: canonicalName, encoding: candidateEncoding, preferred: preferred})
	}
	_, charsetLabel, certain := charset.DetermineEncoding(htmlBytes, contentType)
	addCandidate(charsetLabel, certain)
	addCandidate(charsetFromContentType(contentType), true)
	for _, fallbackLabel := range htmlFallbackEncodingLabels {
		addCandidate(fallbackLabel, false)
	}
	return candidates
}

func charsetFromContentType(contentType string) string {
	_, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return parameters["charset"]
}

func decodeWithEncoding(htmlBytes []byte, label string, sourceEncoding encoding.Encoding) (string, bool) {
	if strings.EqualFold(label, "utf-8") {
		if !utf8.Valid(htmlBytes) {
			return "", false
		}
		return string(htmlBytes), true
	}
	reader := sourceEncoding.NewDecoder().Reader(bytes.NewReader(htmlBytes))
	decodedBytes, err := io.ReadAll(reader)
	if err != nil {
		return "", false
	}
	return string(decodedBytes), true
}

func scoreDecodedHTMLText(decodedText string) int {
	score := 0
	asciiLatinLetters := 0
	extendedLatinLetters := 0
	cyrillicLetters := 0
	nonLatinLetters := 0
	uppercaseLetters := 0
	letters := 0
	replacementCharacters := 0
	controlCharacters := 0
	zeroRunes := 0
	for _, decodedRune := range decodedText {
		switch {
		case decodedRune == utf8.RuneError:
			replacementCharacters++
			score -= 300
		case decodedRune == 0:
			zeroRunes++
			score -= 500
		case decodedRune == '\n' || decodedRune == '\r' || decodedRune == '\t':
			score += 1
		case unicode.IsControl(decodedRune):
			controlCharacters++
			score -= 80
		case unicode.IsLetter(decodedRune):
			letters++
			score += 4
			if unicode.IsUpper(decodedRune) {
				uppercaseLetters++
			}
			switch {
			case decodedRune >= 'A' && decodedRune <= 'Z' || decodedRune >= 'a' && decodedRune <= 'z':
				asciiLatinLetters++
				score += 2
			case unicode.In(decodedRune, unicode.Latin):
				extendedLatinLetters++
			case unicode.In(decodedRune, unicode.Cyrillic):
				cyrillicLetters++
				nonLatinLetters++
				score += 4
			case unicode.In(decodedRune, unicode.Greek, unicode.Hebrew, unicode.Arabic, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
				nonLatinLetters++
				score += 4
			default:
				score += 1
			}
		case unicode.IsDigit(decodedRune), unicode.IsSpace(decodedRune):
			score += 1
		case strings.ContainsRune("<>/=\"'-._:;,&()[]{}#!?%@+*", decodedRune):
			score += 1
		}
	}
	if letters > 0 && nonLatinLetters > letters/3 {
		score += nonLatinLetters * 3
	}
	if extendedLatinLetters > 8 && extendedLatinLetters > asciiLatinLetters/2 {
		score -= extendedLatinLetters * 6
	}
	if letters > 20 && uppercaseLetters*3 > letters*2 {
		score -= uppercaseLetters * 2
	}
	if cyrillicLetters > 10 {
		score += scoreRussianTextShape(decodedText)
	}
	if replacementCharacters > 0 || controlCharacters > 0 || zeroRunes > 0 {
		score -= (replacementCharacters + controlCharacters + zeroRunes) * 100
	}
	return score
}

func scoreRussianTextShape(decodedText string) int {
	score := 0
	for _, fragment := range []string{"Р°", "Р±", "РІ", "Рі", "Рґ", "Рµ", "Р¶", "Р·", "Рё", "Р¹", "Рє", "Р»", "Рј", "РЅ", "Рѕ", "Рї", "СЂ", "СЃ", "С‚", "Сѓ", "С„", "С…", "С†", "С‡", "С€", "С‰", "С‹", "СЊ", "СЌ", "СЋ", "СЏ"} {
		score -= strings.Count(decodedText, fragment) * 120
	}
	lowerText := strings.ToLower(decodedText)
	for _, fragment := range []string{" и ", " в ", " на", " по", "ст", "ен", "то", "ов", "ни", "ра", "ко", "но", "ре", "ос", "ли", "пр", "для", "что", "это", "как", "или", "его"} {
		score += strings.Count(lowerText, fragment) * 35
	}
	for _, decodedRune := range lowerText {
		if strings.ContainsRune("оеаинтсрвлкмдпуяыьгзбчйхжюшцэфъё", decodedRune) {
			score += 2
		}
	}
	return score
}

func isDocumentAttribute(tagName, attributeName string) bool {
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

func pathExt(rawPath string) string {
	lastSlashIndex := strings.LastIndex(rawPath, "/")
	lastDotIndex := strings.LastIndex(rawPath, ".")
	if lastDotIndex <= lastSlashIndex {
		return ""
	}
	return rawPath[lastDotIndex:]
}
