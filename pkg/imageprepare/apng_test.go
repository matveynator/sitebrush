package imageprepare

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"testing"
)

func testAnimatedPNG(t *testing.T, defaultIncluded bool) []byte {
	t.Helper()
	first := image.NewNRGBA(image.Rect(0, 0, 200, 100))
	draw.Draw(first, first.Bounds(), image.NewUniform(color.NRGBA{R: 255, A: 255}), image.Point{}, draw.Src)
	second := image.NewNRGBA(image.Rect(0, 0, 50, 50))
	second.SetNRGBA(20, 10, color.NRGBA{B: 255, A: 128})
	animation := []image.Image{first, second}
	var output bytes.Buffer
	output.WriteString("\x89PNG\r\n\x1a\n")
	frameChunks := make([][]pngChunk, 2)
	for index, frame := range animation {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, pngAnimationImage{Image: frame}); err != nil {
			t.Fatal(err)
		}
		chunks, err := pngChunks(encoded.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		frameChunks[index] = chunks
	}
	writePNGChunk(&output, "IHDR", frameChunks[0][0].payload)
	control := make([]byte, 8)
	binary.BigEndian.PutUint32(control, 2)
	binary.BigEndian.PutUint32(control[4:], 3)
	writePNGChunk(&output, "acTL", control)
	if !defaultIncluded {
		fallback := image.NewNRGBA(first.Bounds())
		draw.Draw(fallback, fallback.Bounds(), image.NewUniform(color.NRGBA{G: 255, A: 255}), image.Point{}, draw.Src)
		var encoded bytes.Buffer
		png.Encode(&encoded, pngAnimationImage{Image: fallback})
		chunks, err := pngChunks(encoded.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		for _, chunk := range chunks {
			if chunk.kind == "IDAT" {
				writePNGChunk(&output, "IDAT", chunk.payload)
			}
		}
	}
	sequence := uint32(0)
	for index, frame := range animation {
		control := make([]byte, 26)
		binary.BigEndian.PutUint32(control, sequence)
		sequence++
		binary.BigEndian.PutUint32(control[4:], uint32(frame.Bounds().Dx()))
		binary.BigEndian.PutUint32(control[8:], uint32(frame.Bounds().Dy()))
		if index == 0 {
			binary.BigEndian.PutUint16(control[20:], 1)
			binary.BigEndian.PutUint16(control[22:], 60)
		} else {
			binary.BigEndian.PutUint32(control[12:], 50)
			binary.BigEndian.PutUint32(control[16:], 25)
			binary.BigEndian.PutUint16(control[20:], 7)
			control[24] = 2
		}
		writePNGChunk(&output, "fcTL", control)
		for _, chunk := range frameChunks[index] {
			if chunk.kind != "IDAT" {
				continue
			}
			if index == 0 && defaultIncluded {
				writePNGChunk(&output, "IDAT", chunk.payload)
			} else {
				pixels := make([]byte, 4+len(chunk.payload))
				binary.BigEndian.PutUint32(pixels, sequence)
				sequence++
				copy(pixels[4:], chunk.payload)
				writePNGChunk(&output, "fdAT", pixels)
			}
		}
	}
	writePNGChunk(&output, "IEND", nil)
	return output.Bytes()
}

func TestAnimatedPNGCropPreservesFramesAndExactTiming(t *testing.T) {
	for _, included := range []bool{false, true} {
		source := testAnimatedPNG(t, included)
		result := Prepare(source, "image/png", Options{Width: 50, Height: 50, Fit: "cover", PositionX: .5, PositionY: .5, Full: true}, nil)
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		animation, err := parsePNGAnimation(result.Preview.Content)
		if err != nil {
			t.Fatal(err)
		}
		if len(animation.frames) != 2 || animation.plays != 3 || binary.BigEndian.Uint16(animation.frames[0].control[20:]) != 1 || binary.BigEndian.Uint16(animation.frames[0].control[22:]) != 60 || binary.BigEndian.Uint16(animation.frames[1].control[22:]) != 0 {
			t.Fatal("animation timing or repeats changed")
		}
		frames, _, _, err := decodePNGAnimation(result.Preview.Content, nil)
		if err != nil {
			t.Fatal(err)
		}
		if frames[0].Bounds() != image.Rect(0, 0, 100, 100) {
			t.Fatal("PNG crop dimensions changed")
		}
		red, green, _, alpha := frames[0].At(0, 0).RGBA()
		if red < 60000 || green > 1000 || alpha != 65535 {
			t.Fatal("default fallback became an animation frame")
		}
		_, _, _, alpha = frames[1].At(0, 25).RGBA()
		if alpha != 0 {
			t.Fatalf("PNG transparency changed: %d", alpha)
		}
		if result.Full == nil || result.Full.Width != 200 || result.Full.Height != 100 {
			t.Fatal("full animation resolution changed")
		}
		full, _, _, err := decodePNGAnimation(result.Full.Content, nil)
		if err != nil || len(full) != 2 {
			t.Fatal("full animation was flattened")
		}
	}
}

func TestAnimatedPNGRejectsCorruptControlBeforeAllocation(t *testing.T) {
	source := testAnimatedPNG(t, true)
	chunks, err := pngChunks(source)
	if err != nil {
		t.Fatal(err)
	}
	var duplicate bytes.Buffer
	duplicate.WriteString("\x89PNG\r\n\x1a\n")
	for _, chunk := range chunks {
		writePNGChunk(&duplicate, chunk.kind, chunk.payload)
		if chunk.kind == "IHDR" {
			header := bytes.Clone(chunk.payload)
			binary.BigEndian.PutUint32(header, 1<<30)
			writePNGChunk(&duplicate, "IHDR", header)
		}
	}
	if Prepare(duplicate.Bytes(), "image/png", Options{Width: 50, Height: 50, Fit: "cover"}, nil).Err == nil {
		t.Fatal("duplicate header bypassed allocation budget")
	}
	source[len(source)-1] ^= 1
	if Prepare(source, "image/png", Options{Width: 50, Height: 50, Fit: "cover"}, nil).Err == nil {
		t.Fatal("corrupt animation accepted")
	}
}
