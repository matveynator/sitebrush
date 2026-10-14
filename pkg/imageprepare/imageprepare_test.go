package imageprepare

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/gen2brain/vpx/webp"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			shade := color.NRGBA{G: 255, A: 255}
			if x < 100 {
				shade = color.NRGBA{R: 255, A: 255}
			}
			if x >= 300 {
				shade = color.NRGBA{B: 255, A: 255}
			}
			source.SetNRGBA(x, y, shade)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestPrepareCropsPixelsAndKeepsFullOnlyWhenRequested(t *testing.T) {
	source := testPNG(t)
	for _, position := range []float64{0, .5, 1} {
		result := Prepare(source, "image/png", Options{Width: 50, Height: 50, Fit: "cover", PositionX: position, PositionY: .5}, nil)
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		decoded, err := png.Decode(bytes.NewReader(result.Preview.Content))
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Bounds() != image.Rect(0, 0, 100, 100) || result.Full != nil {
			t.Fatalf("unexpected result: %+v", result.Preview)
		}
		if len(result.Preview.Content) >= len(source) {
			t.Fatal("icon was not reduced")
		}
		pixel := color.NRGBAModel.Convert(decoded.At(5, 50)).(color.NRGBA)
		if position == 0 && pixel.R < 240 {
			t.Fatalf("left crop lost red pixels: %v", pixel)
		}
		if position == .5 && pixel.G < 240 {
			t.Fatalf("center crop retained outside band: %v", pixel)
		}
	}
	full := Prepare(source, "image/png", Options{Width: 50, Height: 50, Fit: "cover", PositionX: .5, PositionY: .5, Full: true}, nil)
	if full.Err != nil || full.Full == nil || full.Full.Width != 400 || full.Full.Height != 200 {
		t.Fatalf("full size was not preserved: %+v", full)
	}
	contained := Prepare(source, "image/png", Options{Width: 50, Height: 50, Fit: "contain", PositionX: .5, PositionY: .5}, nil)
	if contained.Err != nil || contained.Preview.Width != 100 || contained.Preview.Height != 50 {
		t.Fatalf("contain distorted proportions: %+v", contained.Preview)
	}
}

func TestPrepareAnimatedGIFAndWebPKeepsFramesTimingAndLoop(t *testing.T) {
	palette := color.Palette{color.Transparent, color.NRGBA{R: 255, A: 255}, color.NRGBA{B: 255, A: 255}}
	first := image.NewPaletted(image.Rect(0, 0, 200, 100), palette)
	second := image.NewPaletted(first.Bounds(), palette)
	for index := range first.Pix {
		first.Pix[index] = 1
		second.Pix[index] = 2
	}
	animation := &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{7, 13}, LoopCount: 0, Disposal: []byte{gif.DisposalBackground, gif.DisposalPrevious}}
	var original bytes.Buffer
	if err := gif.EncodeAll(&original, animation); err != nil {
		t.Fatal(err)
	}
	for _, mime := range []string{"image/gif", "image/webp"} {
		result := Prepare(original.Bytes(), mime, Options{Width: 25, Height: 25, Fit: "cover", PositionX: .5, PositionY: .5}, nil)
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		decoded, err := webp.DecodeAll(bytes.NewReader(result.Preview.Content))
		if err != nil {
			t.Fatal(err)
		}
		if len(decoded.Image) != 2 || decoded.Delay[0] != 70 || decoded.Delay[1] != 130 || decoded.LoopCount != 0 || decoded.Image[0].Bounds() != image.Rect(0, 0, 50, 50) {
			t.Fatalf("animation changed: %+v", decoded)
		}
		red, _, blue, _ := decoded.Image[0].At(25, 25).RGBA()
		if red < 60000 || blue > 1000 {
			t.Fatal("first frame lost")
		}
		red, _, blue, _ = decoded.Image[1].At(25, 25).RGBA()
		if blue < 60000 || red > 1000 {
			t.Fatal("second frame lost")
		}
		original.Reset()
		original.Write(result.Preview.Content)
	}
}

func TestSVGReplacementKeepsVectorAnimationAndCrop(t *testing.T) {
	source := []byte(`<svg xmlns="http://www.w3.org/2000/svg" xmlns:custom="urn:example" width="400" height="200" viewBox="0 0 400 200"><rect custom:label="keep" width="400" height="200"><animate attributeName="fill" values="red;blue" dur="1s" repeatCount="indefinite"/></rect></svg>`)
	result := Prepare(source, "image/svg+xml", Options{Width: 50, Height: 50, Fit: "cover", PositionX: .5, PositionY: .5, Full: true}, nil)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if !strings.Contains(string(result.Preview.Content), `viewBox="100 0 200 200"`) || !strings.Contains(string(result.Preview.Content), "<animate") || !strings.Contains(string(result.Preview.Content), `xmlns:custom="urn:example"`) {
		t.Fatalf("SVG crop or animation lost: %s", result.Preview.Content)
	}
	var parsed any
	if err := xml.Unmarshal(result.Preview.Content, &parsed); err != nil {
		t.Fatal(err)
	}
	if result.Full == nil || result.Full.Width != 400 || result.Full.Height != 200 {
		t.Fatal("full SVG dimensions changed")
	}
	for _, malformed := range []string{`<svg><path>`, `<svg/>extra`, `<html></html>`} {
		if Prepare([]byte(malformed), "image/svg+xml", Options{Width: 50, Height: 50, Fit: "cover"}, nil).Err == nil {
			t.Fatalf("accepted malformed SVG %q", malformed)
		}
	}
}

func TestImagePreparationBoundsAndConsumerLifetime(t *testing.T) {
	session := make(chan struct{})
	close(session)
	if result := Prepare(testPNG(t), "image/png", Options{Width: 50, Height: 50, Fit: "cover"}, session); result.Err != ErrCancelled {
		t.Fatal("cancelled consumer was processed")
	}
	malicious := []byte{'G', 'I', 'F', '8', '9', 'a', 255, 255, 255, 255, 0, 0, 0, 0x2c, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 2, 0x44, 1, 0, 0x3b}
	if result := Prepare(malicious, "image/gif", Options{Width: 50, Height: 50, Fit: "cover"}, nil); result.Err == nil {
		t.Fatal("unbounded GIF admitted")
	}
	stop := make(chan struct{})
	jobs := Start(stop)
	defer close(stop)
	reply := make(chan Result, 1)
	jobs <- Job{Source: []byte("invalid"), Options: Options{Width: 50, Height: 50, Fit: "cover"}, Reply: reply, Session: session}
	jobs <- Job{Source: testPNG(t), MIME: "image/png", Options: Options{Width: 50, Height: 50, Fit: "cover", PositionX: .5, PositionY: .5}, Reply: reply}
	select {
	case result := <-reply:
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled job blocked worker")
	}
}

type boundedUniform struct {
	*image.Uniform
	rectangle image.Rectangle
}

func (source boundedUniform) Bounds() image.Rectangle { return source.rectangle }

func TestPrepareAcceptsCameraPhotoAndPreservesTransparency(t *testing.T) {
	var encoded bytes.Buffer
	photo := boundedUniform{Uniform: image.NewUniform(color.NRGBA{R: 80, G: 130, B: 200, A: 255}), rectangle: image.Rect(0, 0, 6000, 4000)}
	if err := jpeg.Encode(&encoded, photo, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	result := Prepare(encoded.Bytes(), "image/jpeg", Options{Width: 50, Height: 50, Fit: "cover", PositionX: .5, PositionY: .5}, nil)
	if result.Err != nil || result.Preview.Width != 100 || result.Preview.Height != 100 {
		t.Fatalf("camera photo replacement failed: %+v", result)
	}
	transparent := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	transparent.SetNRGBA(50, 50, color.NRGBA{R: 255, A: 128})
	encoded.Reset()
	if err := png.Encode(&encoded, transparent); err != nil {
		t.Fatal(err)
	}
	result = Prepare(encoded.Bytes(), "image/png", Options{Width: 50, Height: 50, Fit: "cover", PositionX: .5, PositionY: .5}, nil)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	decoded, err := png.Decode(bytes.NewReader(result.Preview.Content))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, alpha := decoded.At(50, 50).RGBA(); alpha != 128*257 {
		t.Fatalf("alpha changed: %d", alpha)
	}
}

func TestPrepareIconAndEmptySVG(t *testing.T) {
	preview := Prepare(testPNG(t), "image/png", Options{Width: 50, Height: 25, Fit: "contain", PositionX: .5, PositionY: .5}, nil)
	if preview.Err != nil {
		t.Fatal(preview.Err)
	}
	source := make([]byte, 22+len(preview.Preview.Content))
	source[2] = 1
	source[4] = 1
	source[6] = 100
	source[7] = 50
	binary.LittleEndian.PutUint32(source[14:], uint32(len(preview.Preview.Content)))
	binary.LittleEndian.PutUint32(source[18:], 22)
	copy(source[22:], preview.Preview.Content)
	result := Prepare(source, "image/vnd.microsoft.icon", Options{Width: 25, Height: 25, Fit: "cover", PositionX: .5, PositionY: .5}, nil)
	if result.Err != nil || result.Preview.Width != 50 || result.Preview.Height != 50 {
		t.Fatalf("icon crop failed: %+v", result)
	}
	result = Prepare([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="20pt" height="10pt"/><!-- footer -->`), "image/svg+xml", Options{Width: 25, Height: 25, Fit: "cover", PositionX: .5, PositionY: .5}, nil)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	var parsed any
	if err := xml.Unmarshal(result.Preview.Content, &parsed); err != nil {
		t.Fatal(err)
	}
}

func TestGIFPreparationKeepsTransparentBackground(t *testing.T) {
	palette := color.Palette{color.Transparent, color.NRGBA{R: 255, A: 255}}
	first := image.NewPaletted(image.Rect(0, 0, 100, 100), palette)
	first.SetColorIndex(50, 50, 1)
	second := image.NewPaletted(first.Bounds(), palette)
	second.SetColorIndex(25, 25, 1)
	var source bytes.Buffer
	if err := gif.EncodeAll(&source, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{10, 20}, Disposal: []byte{gif.DisposalBackground, gif.DisposalBackground}}); err != nil {
		t.Fatal(err)
	}
	result := Prepare(source.Bytes(), "image/gif", Options{Width: 50, Height: 50, Fit: "cover", PositionX: .5, PositionY: .5}, nil)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	decoded, err := webp.DecodeAll(bytes.NewReader(result.Preview.Content))
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range decoded.Image {
		if _, _, _, alpha := frame.At(0, 0).RGBA(); alpha != 0 {
			t.Fatal("transparent GIF received an opaque background")
		}
	}
}
