package imageprepare

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

type pngChunk struct {
	kind    string
	payload []byte
}
type pngFrame struct {
	control []byte
	pixels  [][]byte
}
type pngAnimation struct {
	header []byte
	shared []pngChunk
	frames []pngFrame
	plays  uint32
}

// APNG reuses the standard PNG decoder for frame pixels. Container control is
// kept separately so cropping does not flatten animation or round its timing.
// https://www.w3.org/TR/png-3/#11fcTL
func pngChunks(source []byte) ([]pngChunk, error) {
	if len(source) < 8 || !bytes.Equal(source[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return nil, errors.New("invalid PNG signature")
	}
	chunks := []pngChunk{}
	for offset := 8; offset+12 <= len(source); {
		size := uint64(binary.BigEndian.Uint32(source[offset:]))
		next := uint64(offset) + size + 12
		if next > uint64(len(source)) || len(chunks) >= 8192 {
			return nil, errors.New("PNG chunks exceed preparation budget")
		}
		end := int(next)
		if crc32.ChecksumIEEE(source[offset+4:end-4]) != binary.BigEndian.Uint32(source[end-4:]) {
			return nil, errors.New("invalid PNG checksum")
		}
		chunk := pngChunk{kind: string(source[offset+4 : offset+8]), payload: source[offset+8 : end-4]}
		chunks = append(chunks, chunk)
		offset = end
		if chunk.kind == "IEND" {
			return chunks, nil
		}
	}
	return nil, errors.New("incomplete PNG")
}

func pngFrameCount(source []byte) (int, error) {
	chunks, err := pngChunks(source)
	if err != nil {
		return 0, err
	}
	frames := 0
	declared := uint32(0)
	for _, chunk := range chunks {
		switch chunk.kind {
		case "acTL":
			if len(chunk.payload) != 8 {
				return 0, errors.New("invalid PNG animation control")
			}
			declared = binary.BigEndian.Uint32(chunk.payload)
		case "fcTL":
			frames++
		}
	}
	if declared == 0 && frames == 0 {
		return 1, nil
	}
	if uint64(frames) != uint64(declared) || frames == 0 {
		return 0, errors.New("invalid PNG frame count")
	}
	return frames, nil
}

func parsePNGAnimation(source []byte) (pngAnimation, error) {
	chunks, err := pngChunks(source)
	if err != nil {
		return pngAnimation{}, err
	}
	animation := pngAnimation{}
	sequence := uint32(0)
	seenPixels := false
	declared := uint32(0)
	for _, chunk := range chunks {
		switch chunk.kind {
		case "IHDR":
			if len(animation.header) != 0 || len(chunk.payload) != 13 {
				return animation, errors.New("invalid PNG header")
			}
			animation.header = chunk.payload
		case "acTL":
			if declared != 0 || seenPixels || len(chunk.payload) != 8 {
				return animation, errors.New("misplaced PNG animation control")
			}
			declared = binary.BigEndian.Uint32(chunk.payload)
			animation.plays = binary.BigEndian.Uint32(chunk.payload[4:])
		case "fcTL":
			if len(chunk.payload) != 26 || binary.BigEndian.Uint32(chunk.payload) != sequence || chunk.payload[24] > 2 || chunk.payload[25] > 1 {
				return animation, errors.New("invalid PNG frame control")
			}
			sequence++
			animation.frames = append(animation.frames, pngFrame{control: chunk.payload})
		case "IDAT":
			seenPixels = true
			if len(animation.frames) == 1 {
				animation.frames[0].pixels = append(animation.frames[0].pixels, chunk.payload)
			}
		case "fdAT":
			if len(chunk.payload) < 4 || binary.BigEndian.Uint32(chunk.payload) != sequence || len(animation.frames) == 0 {
				return animation, errors.New("invalid PNG frame data")
			}
			sequence++
			frame := &animation.frames[len(animation.frames)-1]
			frame.pixels = append(frame.pixels, chunk.payload[4:])
		case "PLTE", "tRNS", "iCCP", "gAMA", "cHRM", "sRGB":
			animation.shared = append(animation.shared, chunk)
		default:
			if chunk.kind != "IEND" && chunk.kind[0] >= 'A' && chunk.kind[0] <= 'Z' {
				return animation, errors.New("unsupported critical PNG chunk")
			}
		}
	}
	if len(animation.header) != 13 || declared == 0 || uint64(declared) != uint64(len(animation.frames)) {
		return animation, errors.New("invalid PNG animation")
	}
	return animation, nil
}

func writePNGChunk(destination *bytes.Buffer, kind string, payload []byte) {
	header := [4]byte{}
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	destination.Write(header[:])
	destination.WriteString(kind)
	destination.Write(payload)
	checksum := crc32.NewIEEE()
	checksum.Write([]byte(kind))
	checksum.Write(payload)
	binary.BigEndian.PutUint32(header[:], checksum.Sum32())
	destination.Write(header[:])
}

func decodePNGAnimation(source []byte, session <-chan struct{}) ([]image.Image, []int, int, error) {
	animation, err := parsePNGAnimation(source)
	if err != nil {
		return nil, nil, 0, err
	}
	width, height := int(binary.BigEndian.Uint32(animation.header)), int(binary.BigEndian.Uint32(animation.header[4:]))
	canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
	previous := image.NewNRGBA(canvas.Bounds())
	frames := make([]image.Image, 0, len(animation.frames))
	delays := make([]int, 0, len(animation.frames))
	for _, frame := range animation.frames {
		if cancelled(session) {
			return nil, nil, 0, ErrCancelled
		}
		frameWidth, frameHeight := uint64(binary.BigEndian.Uint32(frame.control[4:])), uint64(binary.BigEndian.Uint32(frame.control[8:]))
		left, top := uint64(binary.BigEndian.Uint32(frame.control[12:])), uint64(binary.BigEndian.Uint32(frame.control[16:]))
		if frameWidth == 0 || frameHeight == 0 || left+frameWidth > uint64(width) || top+frameHeight > uint64(height) || len(frame.pixels) == 0 {
			return nil, nil, 0, errors.New("invalid PNG frame bounds")
		}
		header := bytes.Clone(animation.header)
		binary.BigEndian.PutUint32(header, uint32(frameWidth))
		binary.BigEndian.PutUint32(header[4:], uint32(frameHeight))
		var encoded bytes.Buffer
		encoded.WriteString("\x89PNG\r\n\x1a\n")
		writePNGChunk(&encoded, "IHDR", header)
		for _, chunk := range animation.shared {
			writePNGChunk(&encoded, chunk.kind, chunk.payload)
		}
		for _, pixels := range frame.pixels {
			writePNGChunk(&encoded, "IDAT", pixels)
		}
		writePNGChunk(&encoded, "IEND", nil)
		decoded, err := png.Decode(bytes.NewReader(encoded.Bytes()))
		if err != nil {
			return nil, nil, 0, err
		}
		rectangle := image.Rect(int(left), int(top), int(left+frameWidth), int(top+frameHeight))
		draw.Draw(previous, previous.Bounds(), canvas, image.Point{}, draw.Src)
		blend := draw.Src
		if frame.control[25] == 1 {
			blend = draw.Over
		}
		draw.Draw(canvas, rectangle, decoded, image.Point{}, blend)
		complete := image.NewNRGBA(canvas.Bounds())
		draw.Draw(complete, complete.Bounds(), canvas, image.Point{}, draw.Src)
		frames = append(frames, complete)
		numerator, denominator := int(binary.BigEndian.Uint16(frame.control[20:])), int(binary.BigEndian.Uint16(frame.control[22:]))
		if denominator == 0 {
			denominator = 100
		}
		delays = append(delays, (numerator*1000+denominator/2)/denominator)
		switch frame.control[24] {
		case 1:
			draw.Draw(canvas, rectangle, image.NewUniform(color.Transparent), image.Point{}, draw.Src)
		case 2:
			draw.Draw(canvas, canvas.Bounds(), previous, image.Point{}, draw.Src)
		}
	}
	return frames, delays, int(animation.plays), nil
}

// Frames share one RGBA header even when individual frames happen to be opaque.
type pngAnimationImage struct{ image.Image }

func (source pngAnimationImage) Opaque() bool { return false }

func encodePNGAnimation(frames []image.Image, source []byte, session <-chan struct{}) (File, error) {
	original, err := parsePNGAnimation(source)
	if err != nil {
		return File{}, err
	}
	if len(frames) != len(original.frames) {
		return File{}, errors.New("PNG frame count changed")
	}
	var output bytes.Buffer
	output.WriteString("\x89PNG\r\n\x1a\n")
	sequence := uint32(0)
	for index, frame := range frames {
		if cancelled(session) {
			return File{}, ErrCancelled
		}
		var encoded bytes.Buffer
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		if err = encoder.Encode(&encoded, pngAnimationImage{Image: frame}); err != nil {
			return File{}, err
		}
		chunks, err := pngChunks(encoded.Bytes())
		if err != nil {
			return File{}, err
		}
		if index == 0 {
			writePNGChunk(&output, "IHDR", chunks[0].payload)
			for _, chunk := range original.shared {
				if chunk.kind == "gAMA" || chunk.kind == "cHRM" || chunk.kind == "sRGB" || chunk.kind == "iCCP" && original.header[9] != 0 && original.header[9] != 4 {
					writePNGChunk(&output, chunk.kind, chunk.payload)
				}
			}
			control := make([]byte, 8)
			binary.BigEndian.PutUint32(control, uint32(len(frames)))
			binary.BigEndian.PutUint32(control[4:], original.plays)
			writePNGChunk(&output, "acTL", control)
		}
		control := make([]byte, 26)
		binary.BigEndian.PutUint32(control, sequence)
		sequence++
		binary.BigEndian.PutUint32(control[4:], uint32(frame.Bounds().Dx()))
		binary.BigEndian.PutUint32(control[8:], uint32(frame.Bounds().Dy()))
		copy(control[20:24], original.frames[index].control[20:24])
		writePNGChunk(&output, "fcTL", control)
		for _, chunk := range chunks {
			if chunk.kind != "IDAT" {
				continue
			}
			if index == 0 {
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
	return File{Content: output.Bytes(), MIME: "image/png", Extension: ".png", Width: frames[0].Bounds().Dx(), Height: frames[0].Bounds().Dy()}, nil
}
