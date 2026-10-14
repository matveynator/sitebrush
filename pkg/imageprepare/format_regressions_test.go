package imageprepare

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"strings"
	"testing"
	"time"
)

func iconBitmap(bits uint16) []byte {
	bitmap := make([]byte, 64)
	binary.LittleEndian.PutUint32(bitmap, 40)
	binary.LittleEndian.PutUint32(bitmap[4:], 2)
	binary.LittleEndian.PutUint32(bitmap[8:], 4)
	binary.LittleEndian.PutUint16(bitmap[12:], 1)
	binary.LittleEndian.PutUint16(bitmap[14:], bits)
	if bits == 8 {
		binary.LittleEndian.PutUint32(bitmap[32:], 2)
		bitmap[46] = 255
		copy(bitmap[48:], []byte{1, 1, 0, 0, 1, 1, 0, 0})
	} else {
		pixelBytes := int(bits / 8)
		for row := 0; row < 2; row++ {
			for column := 0; column < 2; column++ {
				bitmap[40+row*8+column*pixelBytes+2] = 255
				if bits == 32 {
					bitmap[40+row*8+column*4+3] = 128
				}
			}
		}
	}
	bitmap[60] = 128
	return bitmap
}

func iconContainer(bitmap []byte) []byte {
	icon := make([]byte, 22+len(bitmap))
	binary.LittleEndian.PutUint16(icon[2:], 1)
	binary.LittleEndian.PutUint16(icon[4:], 1)
	icon[6], icon[7] = 2, 2
	binary.LittleEndian.PutUint32(icon[14:], uint32(len(bitmap)))
	binary.LittleEndian.PutUint32(icon[18:], 22)
	copy(icon[22:], bitmap)
	return icon
}

func TestIconBitmapPreservesPaletteAlphaAndTransparencyMask(t *testing.T) {
	for _, bits := range []uint16{8, 24, 32} {
		prepared := Prepare(iconContainer(iconBitmap(bits)), "image/x-icon", Options{Width: 1, Height: 1, Fit: "cover", Full: true}, nil)
		if prepared.Err != nil {
			t.Fatalf("%d-bit icon: %v", bits, prepared.Err)
		}
		decoded, _, err := image.Decode(bytes.NewReader(prepared.Preview.Content))
		if err != nil {
			t.Fatal(err)
		}
		masked := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA)
		visible := color.NRGBAModel.Convert(decoded.At(1, 0)).(color.NRGBA)
		alpha := uint8(255)
		if bits == 32 {
			alpha = 128
		}
		if masked.A != 0 || visible.R != 255 || visible.A != alpha || prepared.Full == nil {
			t.Fatalf("%d-bit icon lost color/alpha: masked=%v visible=%v", bits, masked, visible)
		}
	}
}

func TestBMPAndIconRejectOutOfBoundsPaletteIndex(t *testing.T) {
	dib := iconBitmap(8)
	dib[48] = 255
	bmp := make([]byte, 14+len(dib))
	copy(bmp, "BM")
	binary.LittleEndian.PutUint32(bmp[2:], uint32(len(bmp)))
	binary.LittleEndian.PutUint32(bmp[10:], 62)
	copy(bmp[14:], dib)
	binary.LittleEndian.PutUint32(bmp[22:], 2)
	for _, source := range [][]byte{bmp, iconContainer(dib)} {
		if result := Prepare(source, "", Options{Width: 1, Height: 1, Fit: "cover"}, nil); result.Err == nil {
			t.Fatal("invalid BMP palette index was accepted")
		}
	}
}

func TestIconRejectsMalformedDirectoryAndBitmap(t *testing.T) {
	for _, corrupt := range []func([]byte){
		func(icon []byte) { binary.LittleEndian.PutUint16(icon[4:], 0) },
		func(icon []byte) { binary.LittleEndian.PutUint16(icon[4:], 99) },
		func(icon []byte) { binary.LittleEndian.PutUint32(icon[14:], 0) },
		func(icon []byte) { binary.LittleEndian.PutUint32(icon[18:], 999) },
		func(icon []byte) { binary.LittleEndian.PutUint32(icon[14:], 20) },
		func(icon []byte) { binary.LittleEndian.PutUint32(icon[22:], 39) },
		func(icon []byte) { binary.LittleEndian.PutUint32(icon[26:], 5000) },
		func(icon []byte) { binary.LittleEndian.PutUint32(icon[54:], 257) },
		func(icon []byte) { binary.LittleEndian.PutUint32(icon[54:], 256) },
		func(icon []byte) { binary.LittleEndian.PutUint32(icon[38:], 99) },
	} {
		source := iconContainer(iconBitmap(8))
		corrupt(source)
		if result := Prepare(source, "", Options{Width: 1, Height: 1, Fit: "cover"}, nil); result.Err == nil {
			t.Fatal("malformed icon was accepted")
		}
	}
}

func exifJPEG(orientation uint16, bigEndian bool) []byte {
	encoded := make([]byte, 40)
	copy(encoded, []byte{255, 216, 255, 225, 0, 34})
	copy(encoded[6:], "Exif\x00\x00")
	var order binary.ByteOrder = binary.LittleEndian
	copy(encoded[12:], "II")
	if bigEndian {
		order = binary.BigEndian
		copy(encoded[12:], "MM")
	}
	order.PutUint16(encoded[14:], 42)
	order.PutUint32(encoded[16:], 8)
	order.PutUint16(encoded[20:], 1)
	order.PutUint16(encoded[22:], 0x112)
	order.PutUint16(encoded[24:], 3)
	order.PutUint32(encoded[26:], 1)
	order.PutUint16(encoded[30:], orientation)
	copy(encoded[38:], []byte{255, 217})
	return encoded
}

func TestJPEGOrientationPreservesEveryPixelForBothByteOrders(t *testing.T) {
	source := image.NewNRGBA(image.Rect(10, 20, 12, 23))
	for index := 0; index < 6; index++ {
		source.SetNRGBA(10+index%2, 20+index/2, color.NRGBA{R: uint8(index + 1), A: 255})
	}
	for orientation, pixels := range [][]uint8{
		{1, 2, 3, 4, 5, 6}, {2, 1, 4, 3, 6, 5}, {6, 5, 4, 3, 2, 1}, {5, 6, 3, 4, 1, 2},
		{1, 3, 5, 2, 4, 6}, {5, 3, 1, 6, 4, 2}, {6, 4, 2, 5, 3, 1}, {2, 4, 6, 1, 3, 5},
	} {
		for _, bigEndian := range []bool{false, true} {
			rotated := orientJPEG(source, exifJPEG(uint16(orientation+1), bigEndian))
			bounds := rotated.Bounds()
			for index, red := range pixels {
				pixel := color.NRGBAModel.Convert(rotated.At(bounds.Min.X+index%bounds.Dx(), bounds.Min.Y+index/bounds.Dx())).(color.NRGBA)
				if pixel.R != red {
					t.Fatalf("orientation=%d bigEndian=%t pixel=%d got=%d want=%d", orientation+1, bigEndian, index, pixel.R, red)
				}
			}
		}
	}
	for _, malformed := range [][]byte{
		{255, 216, 255, 218, 0, 0}, {255, 216, 255, 217, 0, 0}, {255, 216, 255, 225, 0, 1},
		{255, 216, 255, 225, 0, 99}, {255, 216, 255, 225, 0, 8, 'E', 'x', 'i', 'f', 0, 0},
	} {
		if jpegOrientation(malformed) != 1 {
			t.Fatal("malformed EXIF changed orientation")
		}
	}
	for _, corrupt := range []func([]byte){
		func(encoded []byte) { copy(encoded[12:], "ZZ") },
		func(encoded []byte) { binary.LittleEndian.PutUint32(encoded[16:], 999) },
		func(encoded []byte) { binary.LittleEndian.PutUint16(encoded[20:], 99); encoded[22] = 0 },
		func(encoded []byte) { encoded[22] = 0 },
	} {
		encoded := exifJPEG(6, false)
		corrupt(encoded)
		if jpegOrientation(encoded) != 1 {
			t.Fatal("invalid EXIF directory changed orientation")
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, nil); err != nil {
		t.Fatal(err)
	}
	withEXIF := append(exifJPEG(6, false)[:38], encoded.Bytes()[2:]...)
	prepared := Prepare(withEXIF, "image/jpeg", Options{Width: 2, Height: 2, Fit: "contain", Full: true}, nil)
	if prepared.Err != nil || prepared.Full == nil || prepared.Full.Width != 3 || prepared.Full.Height != 2 {
		t.Fatalf("JPEG replacement did not use its display orientation: %+v", prepared)
	}
}

func TestSVGEmbeddedRasterOptimizationPreservesVectorMarkup(t *testing.T) {
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG(t))
	svg := `<svg width="400" height="200"><image width="25" height="25" href="` + dataURL + `"/><animate attributeName="opacity" from="0" to="1"/></svg>`
	prepared := Prepare([]byte(svg), "image/svg+xml", Options{Width: 50, Height: 50, Fit: "contain", Full: true}, nil)
	if prepared.Err != nil || bytes.Contains(prepared.Preview.Content, []byte(dataURL)) || !bytes.Contains(prepared.Preview.Content, []byte("<animate")) || prepared.Full == nil {
		t.Fatalf("embedded raster optimization failed: %+v", prepared)
	}
	for _, source := range []string{
		`<image width="0" href="` + dataURL + `"/>`, `<image width="10" height="10" href="https://example.com/image.png"/>`,
		`<image width="10" height="10" href="data:image/png,raw"/>`, `<image width="10" height="10" href="data:image/svg+xml;base64,ignored"/>`,
	} {
		source += "</svg>"
		if next, err := optimizeEmbeddedSVGImages(source, nil); err != nil || next != source {
			t.Fatalf("unrelated image markup changed: %s err=%v", next, err)
		}
	}
	for _, source := range []string{
		`<image`, `<image width="10" height="10" href="data:image/png;base64,%%%"/>`,
		`<image width="10" height="10" href="data:image/png;base64,aW52YWxpZA=="/>`,
	} {
		source += "</svg>"
		if _, err := optimizeEmbeddedSVGImages(source, nil); err == nil {
			t.Fatal("malformed embedded raster was accepted")
		}
	}
}

type imageJobReader struct {
	content io.Reader
	closed  chan struct{}
}

func (reader *imageJobReader) Read(destination []byte) (int, error) {
	return reader.content.Read(destination)
}
func (reader *imageJobReader) Close() error { close(reader.closed); return nil }

type failedImageReader struct{}

func (failedImageReader) Read([]byte) (int, error) { return 0, errors.New("source read failed") }

func TestImageWorkerOwnsAndClosesAdmittedReaders(t *testing.T) {
	stop := make(chan struct{})
	jobs := Start(stop)
	defer close(stop)
	for _, source := range []io.Reader{bytes.NewReader(testPNG(t)), failedImageReader{}} {
		reader := &imageJobReader{content: source, closed: make(chan struct{})}
		reply := make(chan Result, 1)
		jobs <- Job{Reader: reader, Options: Options{Width: 10, Height: 10, Fit: "cover"}, Reply: reply}
		select {
		case result := <-reply:
			if _, failed := source.(failedImageReader); failed != (result.Err != nil) {
				t.Fatalf("reader result: %v", result.Err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not reply")
		}
		select {
		case <-reader.closed:
		default:
			t.Fatal("worker did not release its source")
		}
	}
	cancelledSession := make(chan struct{})
	close(cancelledSession)
	reader := &imageJobReader{content: failedImageReader{}, closed: make(chan struct{})}
	jobs <- Job{Reader: reader, Session: cancelledSession}
	select {
	case <-reader.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled reader was not released")
	}
}

func TestImageWorkerShutdownReleasesQueuedReaders(t *testing.T) {
	stop := make(chan struct{})
	jobs := Start(stop)
	pipeReader, pipeWriter := io.Pipe()
	active := &imageJobReader{content: pipeReader, closed: make(chan struct{})}
	jobs <- Job{Reader: active, Options: Options{Width: 1, Height: 1, Fit: "cover"}, Reply: make(chan Result)}
	// An unbuffered reply keeps this first job active until shutdown, whether
	// the worker has started reading its pipe or is still waiting for scheduling.
	queued := &imageJobReader{content: failedImageReader{}, closed: make(chan struct{})}
	reply := make(chan Result, 1)
	jobs <- Job{Reader: queued, Reply: reply}
	close(stop)
	pipeWriter.Close()
	defer pipeReader.Close()
	for _, reader := range []*imageJobReader{active, queued} {
		select {
		case <-reader.closed:
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown retained a reader")
		}
	}
	select {
	case result := <-reply:
		if result.Err != ErrCancelled {
			t.Fatalf("queued job result=%v", result.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued job received no shutdown result")
	}
}

func TestIconEmbeddedPNGDimensionsAreBoundedBeforeDecode(t *testing.T) {
	for _, dimensions := range []int{50000, 2} {
		var encoded bytes.Buffer
		encoded.WriteString("\x89PNG\r\n\x1a\n")
		header := make([]byte, 13)
		binary.BigEndian.PutUint32(header, uint32(dimensions))
		binary.BigEndian.PutUint32(header[4:], uint32(dimensions))
		header[8], header[9] = 8, 6
		writePNGChunk(&encoded, "IHDR", header)
		writePNGChunk(&encoded, "IEND", nil)
		if result := Prepare(iconContainer(encoded.Bytes()), "", Options{Width: 1, Height: 1, Fit: "cover"}, nil); result.Err == nil {
			t.Fatal("oversized or truncated embedded PNG was decoded")
		}
	}
	if result := Prepare(iconContainer([]byte("\x89PNG\r\n\x1a\n")), "", Options{Width: 1, Height: 1, Fit: "cover"}, nil); result.Err == nil {
		t.Fatal("invalid embedded PNG header was accepted")
	}
}

func TestPrepareRejectsInvalidFramesAndOversizedSources(t *testing.T) {
	for _, options := range []Options{
		{Width: 0, Height: 1, Fit: "cover"}, {Width: 12001, Height: 1, Fit: "cover"},
		{Width: 1, Height: 1, Fit: "unknown"}, {Width: 1, Height: 1, Fit: "cover", PositionX: math.NaN()},
		{Width: 1, Height: 1, Fit: "cover", PositionY: 2},
	} {
		if result := Prepare(nil, "", options, nil); result.Err == nil {
			t.Fatal("invalid frame accepted")
		}
	}
	if result := Prepare(make([]byte, SourceByteLimit+1), "", Options{Width: 1, Height: 1, Fit: "cover"}, nil); result.Err == nil || !strings.Contains(result.Err.Error(), "budget") {
		t.Fatal("oversized source accepted")
	}
}
