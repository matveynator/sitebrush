package captcha

import (
	"bytes"
	"crypto/rand"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

func Render(answer string) ([]byte, error) {
	var entropy [4096]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return nil, err
	}
	parsed, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: 48, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	defer face.Close()
	canvas := image.NewRGBA(image.Rect(0, 0, 320, 100))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{246, 247, 249, 255}), image.Point{}, draw.Src)
	for index := 0; index < 1200; index++ {
		x := int(entropy[index*3%len(entropy)]) * 320 / 256
		y := int(entropy[(index*3+1)%len(entropy)]) * 100 / 256
		shade := 120 + entropy[(index*3+2)%len(entropy)]%100
		canvas.SetRGBA(x, y, color.RGBA{shade, shade, shade, 255})
	}
	for index, character := range answer {
		glyph := image.NewRGBA(image.Rect(0, 0, 64, 80))
		ink := color.RGBA{25 + entropy[3600+index]%60, 25 + entropy[3620+index]%60, 30 + entropy[3640+index]%60, 255}
		painter := font.Drawer{Dst: glyph, Src: image.NewUniform(ink), Face: face, Dot: fixed.P(10, 60)}
		painter.DrawString(string(character))
		angle := (float64(entropy[3660+index])/255 - 0.5) * 0.65
		cosine, sine := math.Cos(angle), math.Sin(angle)
		for y := 0; y < 80; y++ {
			for x := 0; x < 64; x++ {
				pixel := glyph.RGBAAt(x, y)
				if pixel.A == 0 {
					continue
				}
				rotatedX := int(cosine*float64(x-32)-sine*float64(y-40)) + 36 + index*49
				rotatedY := int(sine*float64(x-32)+cosine*float64(y-40)) + 48 + int(entropy[3680+index]%12) - 6
				canvas.Set(rotatedX, rotatedY, pixel)
			}
		}
	}
	// Raster distortion and crossing curves prevent extracting text or solving
	// a fixed glyph position. Online guessing is bounded independently in SQL.
	warped := image.NewRGBA(canvas.Bounds())
	for y := 0; y < 100; y++ {
		shift := int(4 * math.Sin(float64(y)/11+float64(entropy[3700])))
		for x := 0; x < 320; x++ {
			warped.Set(x, y, canvas.At((x+shift+320)%320, y))
		}
	}
	for curve := 0; curve < 7; curve++ {
		for x := 0; x < 320; x++ {
			y := int(entropy[3720+curve]%80) + 10 + int(7*math.Sin(float64(x)/23+float64(entropy[3740+curve])))
			warped.Set(x, y, color.RGBA{100, 110, 125, 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, warped); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
