package imageprepare

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

func prepareSVG(source []byte, options Options, session <-chan struct{}) Result {
	decoder := xml.NewDecoder(bytes.NewReader(source))
	var root xml.StartElement
	rootStart, rootEnd := int64(0), int64(0)
	for {
		rootStart = decoder.InputOffset()
		token, err := decoder.Token()
		if err != nil {
			return Result{Err: err}
		}
		if start, ok := token.(xml.StartElement); ok {
			if start.Name.Local != "svg" {
				return Result{Err: errors.New("invalid SVG root")}
			}
			root, rootEnd = start, decoder.InputOffset()
			break
		}
	}
	width, height := float64(0), float64(0)
	for _, attribute := range root.Attr {
		switch attribute.Name.Local {
		case "width":
			width = svgDimension(attribute.Value)
		case "height":
			height = svgDimension(attribute.Value)
		}
	}
	for _, attribute := range root.Attr {
		if attribute.Name.Local == "viewBox" {
			parts := strings.Fields(strings.ReplaceAll(attribute.Value, ",", " "))
			if len(parts) == 4 {
				if width <= 0 {
					width, _ = strconv.ParseFloat(parts[2], 64)
				}
				if height <= 0 {
					height, _ = strconv.ParseFloat(parts[3], 64)
				}
			}
		}
	}
	if width <= 0 {
		width = 300
	}
	if height <= 0 {
		height = 150
	}
	if math.IsNaN(width) || math.IsNaN(height) || math.IsInf(width, 0) || math.IsInf(height, 0) || width > math.MaxInt32 || height > math.MaxInt32 {
		return Result{Err: errors.New("invalid SVG dimensions")}
	}
	// Validate the complete document before preserving its source; processing
	// instructions and external declarations are never needed by an image.
	depth := 1
	for depth > 0 {
		token, err := decoder.Token()
		if err != nil {
			return Result{Err: err}
		}
		switch token.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	documentEnd := decoder.InputOffset()
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Result{Err: err}
		}
		switch trailing := token.(type) {
		case xml.CharData:
			if len(bytes.TrimSpace(trailing)) > 0 {
				return Result{Err: errors.New("extra content after SVG")}
			}
		case xml.Comment, xml.ProcInst:
		default:
			return Result{Err: errors.New("extra content after SVG")}
		}
	}
	if cancelled(session) {
		return Result{Err: ErrCancelled}
	}
	nestedRoot := string(source[rootStart:rootEnd])
	selfClosing := strings.HasSuffix(strings.TrimSpace(nestedRoot), "/>")
	if selfClosing {
		nestedRoot = strings.TrimSuffix(strings.TrimSpace(nestedRoot), "/>") + ">"
	}
	nestedRoot = setSVGAttribute(nestedRoot, "width", strconv.FormatFloat(width, 'f', -1, 64))
	nestedRoot = setSVGAttribute(nestedRoot, "height", strconv.FormatFloat(height, 'f', -1, 64))
	body := string(source[rootEnd:documentEnd])
	if selfClosing {
		body = "</svg>"
	}
	optimizedBody, err := optimizeEmbeddedSVGImages(body, session)
	if err != nil {
		return Result{Err: err}
	}
	cropWidth, cropHeight := width, height
	if options.Fit == "cover" {
		ratio := float64(options.Width) / float64(options.Height)
		if width/height > ratio {
			cropWidth = height * ratio
		} else {
			cropHeight = width / ratio
		}
	}
	left, top := (width-cropWidth)*options.PositionX, (height-cropHeight)*options.PositionY
	preserve := "xMidYMid meet"
	if options.Fit == "fill" {
		preserve = "none"
	}
	preview := []byte(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="%g %g %g %g" preserveAspectRatio="%s" overflow="hidden">`, options.Width*2, options.Height*2, left, top, cropWidth, cropHeight, preserve) + nestedRoot + optimizedBody + "</svg>")
	result := Result{Preview: File{Content: preview, MIME: "image/svg+xml", Extension: ".svg", Width: options.Width * 2, Height: options.Height * 2}}
	if options.Full {
		full := File{Content: source[rootStart:documentEnd], MIME: "image/svg+xml", Extension: ".svg", Width: int(math.Round(width)), Height: int(math.Round(height))}
		result.Full = &full
	}
	return result
}

func svgDimension(dimension string) float64 {
	dimension = strings.TrimSpace(dimension)
	multiplier := float64(1)
	for _, unit := range []struct {
		suffix string
		scale  float64
	}{{"px", 1}, {"pt", 96.0 / 72}, {"pc", 16}, {"in", 96}, {"cm", 96 / 2.54}, {"mm", 96 / 25.4}} {
		if strings.HasSuffix(dimension, unit.suffix) {
			dimension = strings.TrimSuffix(dimension, unit.suffix)
			multiplier = unit.scale
			break
		}
	}
	parsed, err := strconv.ParseFloat(dimension, 64)
	if err != nil {
		return 0
	}
	return parsed * multiplier
}

// Replace only a root attribute; descendants retain animation, CSS and IDs.
func setSVGAttribute(startTag, name, replacement string) string {
	attributePattern := regexp.MustCompile(`\s` + regexp.QuoteMeta(name) + `\s*=\s*(?:"[^"]*"|'[^']*')`)
	cleaned := attributePattern.ReplaceAllString(startTag, "")
	return strings.TrimSuffix(cleaned, ">") + ` ` + name + `="` + replacement + `">`
}

func optimizeEmbeddedSVGImages(source string, session <-chan struct{}) (string, error) {
	// XML offsets allow replacing data URLs without reserializing unrelated SVG.
	decoder := xml.NewDecoder(strings.NewReader("<svg>" + source))
	replacements := []struct{ old, next string }{}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "image" {
			continue
		}
		width, height := 0, 0
		for _, attribute := range start.Attr {
			if attribute.Name.Local == "width" {
				width = int(math.Ceil(svgDimension(attribute.Value)))
			}
			if attribute.Name.Local == "height" {
				height = int(math.Ceil(svgDimension(attribute.Value)))
			}
		}
		if width <= 0 || height <= 0 {
			continue
		}
		for _, attribute := range start.Attr {
			if attribute.Name.Local != "href" || !strings.HasPrefix(attribute.Value, "data:image/") {
				continue
			}
			header, payload, found := strings.Cut(attribute.Value, ",")
			if !found || !strings.HasSuffix(header, ";base64") || strings.Contains(header, "svg+xml") {
				continue
			}
			encoded, decodeErr := base64.StdEncoding.DecodeString(payload)
			if decodeErr != nil {
				return "", decodeErr
			}
			prepared := Prepare(encoded, strings.TrimPrefix(strings.TrimSuffix(header, ";base64"), "data:"), Options{Width: min(width, 12000), Height: min(height, 12000), Fit: "contain", PositionX: .5, PositionY: .5}, session)
			if prepared.Err != nil {
				return "", prepared.Err
			}
			next := "data:" + prepared.Preview.MIME + ";base64," + base64.StdEncoding.EncodeToString(prepared.Preview.Content)
			if len(next) < len(attribute.Value) {
				replacements = append(replacements, struct{ old, next string }{attribute.Value, next})
			}
		}
	}
	for _, replacement := range replacements {
		source = strings.ReplaceAll(source, replacement.old, replacement.next)
	}
	return source, nil
}
