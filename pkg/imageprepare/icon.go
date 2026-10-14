package imageprepare

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
)

// ICO containers carry either PNG or a DIB plus a transparency mask. The
// largest frame is the page replacement source; full-size reuse keeps the ICO.
func decodeIcon(source []byte) (image.Image, error) {
	count := int(binary.LittleEndian.Uint16(source[4:]))
	if count == 0 || 6+count*16 > len(source) {
		return nil, errors.New("invalid icon directory")
	}
	largestArea, selected := 0, 0
	for index := 0; index < count; index++ {
		entry := source[6+index*16 : 6+(index+1)*16]
		width, height := int(entry[0]), int(entry[1])
		if width == 0 {
			width = 256
		}
		if height == 0 {
			height = 256
		}
		if width*height > largestArea {
			largestArea = width * height
			selected = 6 + index*16
		}
	}
	entry := source[selected : selected+16]
	size, offset := uint64(binary.LittleEndian.Uint32(entry[8:])), uint64(binary.LittleEndian.Uint32(entry[12:]))
	if size == 0 || offset+size > uint64(len(source)) {
		return nil, errors.New("invalid icon frame")
	}
	pixels := source[offset : offset+size]
	if bytes.HasPrefix(pixels, []byte("\x89PNG\r\n\x1a\n")) {
		configuration, _, err := image.DecodeConfig(bytes.NewReader(pixels))
		if err != nil {
			return nil, err
		}
		if int64(configuration.Width)*int64(configuration.Height) > DecodedBudget/20 {
			return nil, errors.New("icon exceeds preparation budget")
		}
		decoded, _, err := image.Decode(bytes.NewReader(pixels))
		return decoded, err
	}
	if len(pixels) < 40 {
		return nil, errors.New("invalid icon bitmap")
	}
	headerSize := int(binary.LittleEndian.Uint32(pixels))
	width := int(int32(binary.LittleEndian.Uint32(pixels[4:])))
	height := int(int32(binary.LittleEndian.Uint32(pixels[8:]))) / 2
	bits := int(binary.LittleEndian.Uint16(pixels[14:]))
	if headerSize < 40 || headerSize > len(pixels) || width <= 0 || height <= 0 || width > 4096 || height > 4096 || int64(width)*int64(height) > DecodedBudget/20 {
		return nil, errors.New("invalid icon dimensions")
	}
	colorCount := int(binary.LittleEndian.Uint32(pixels[32:]))
	if colorCount < 0 || colorCount > 256 {
		return nil, errors.New("invalid icon palette")
	}
	if bits <= 8 && colorCount == 0 {
		colorCount = 1 << bits
	}
	pixelOffset := headerSize + colorCount*4
	if pixelOffset < 0 || pixelOffset > len(pixels) {
		return nil, errors.New("invalid icon palette")
	}
	bitmap := make([]byte, 14+len(pixels))
	copy(bitmap[14:], pixels)
	copy(bitmap, "BM")
	binary.LittleEndian.PutUint32(bitmap[2:], uint32(len(bitmap)))
	binary.LittleEndian.PutUint32(bitmap[10:], uint32(14+pixelOffset))
	binary.LittleEndian.PutUint32(bitmap[22:], uint32(height))
	decoded, _, err := image.Decode(bytes.NewReader(bitmap))
	if err != nil {
		return nil, err
	}
	destination := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(destination, destination.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	rowBytes := ((width*bits + 31) / 32) * 4
	maskOffset := pixelOffset + rowBytes*height
	maskRowBytes := ((width + 31) / 32) * 4
	alphaUsed := false
	if bits == 32 && maskOffset <= len(pixels) {
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				if pixels[pixelOffset+y*rowBytes+x*4+3] != 0 {
					alphaUsed = true
				}
			}
		}
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			pixel := destination.NRGBAAt(x, y)
			if bits == 32 && alphaUsed {
				pixel.A = pixels[pixelOffset+(height-1-y)*rowBytes+x*4+3]
			} else {
				pixel.A = 255
			}
			maskIndex := maskOffset + (height-1-y)*maskRowBytes + x/8
			if maskIndex < len(pixels) && pixels[maskIndex]&(128>>uint(x%8)) != 0 {
				pixel.A = 0
			}
			destination.SetNRGBA(x, y, pixel)
		}
	}
	return destination, nil
}
