// Package imageprepare owns bounded image replacement jobs independently of HTTP.
package imageprepare

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"

	"github.com/gen2brain/vpx/webp"
	_ "golang.org/x/image/bmp"
	imagedraw "golang.org/x/image/draw"
)

const DecodedBudget = 256 * 1024 * 1024
const SourceByteLimit = 64 * 1024 * 1024

var ErrCancelled = errors.New("image preparation cancelled")
var ErrBusy = errors.New("image preparation queue is full")

type Options struct {
	Width, Height        int
	Fit                  string
	PositionX, PositionY float64
	Full                 bool
}

type File struct {
	Content         []byte
	MIME, Extension string
	Width, Height   int
}

type Result struct {
	Preview File
	Full    *File
	Err     error
}
type Job struct {
	Source  []byte
	MIME    string
	Options Options
	Reply   chan<- Result
	Session <-chan struct{}
	Reader  io.ReadCloser
}

// A worker owns each admitted source until it replies. Consumers terminate their
// session rather than closing a reply channel while a sender still owns it.
func Start(stop <-chan struct{}) chan<- Job {
	jobs := make(chan Job, 4)
	go func() {
		defer func() {
			for {
				select {
				case job := <-jobs:
					if job.Reader != nil {
						_ = job.Reader.Close()
					}
					select {
					case job.Reply <- Result{Err: ErrCancelled}:
					case <-job.Session:
					default:
					}
				default:
					return
				}
			}
		}()
		for {
			select {
			case <-stop:
				return
			case job := <-jobs:
				if cancelled(job.Session) {
					if job.Reader != nil {
						_ = job.Reader.Close()
					}
					continue
				}
				var readErr error
				if job.Reader != nil {
					job.Source, readErr = io.ReadAll(io.LimitReader(job.Reader, SourceByteLimit+1))
					_ = job.Reader.Close()
				}
				result := Result{Err: readErr}
				if readErr == nil {
					result = Prepare(job.Source, job.MIME, job.Options, job.Session)
				}
				select {
				case job.Reply <- result:
				case <-job.Session:
				case <-stop:
					return
				}
			}
		}
	}()
	return jobs
}

func cancelled(session <-chan struct{}) bool {
	select {
	case <-session:
		return true
	default:
		return false
	}
}

func Prepare(source []byte, mime string, options Options, session <-chan struct{}) Result {
	if cancelled(session) {
		return Result{Err: ErrCancelled}
	}
	if len(source) > SourceByteLimit {
		return Result{Err: errors.New("image source exceeds preparation budget")}
	}
	if options.Width <= 0 || options.Height <= 0 || options.Width > 12000 || options.Height > 12000 || math.IsNaN(options.PositionX) || math.IsNaN(options.PositionY) || options.PositionX < 0 || options.PositionX > 1 || options.PositionY < 0 || options.PositionY > 1 {
		return Result{Err: errors.New("invalid image frame")}
	}
	if options.Fit != "cover" && options.Fit != "contain" && options.Fit != "fill" {
		return Result{Err: errors.New("invalid image fit")}
	}
	if mime == "image/svg+xml" || bytes.HasPrefix(bytes.TrimSpace(source[:min(len(source), 1024)]), []byte("<")) {
		return prepareSVG(source, options, session)
	}
	frames, delays, loops, format, err := decodeFrames(source, session)
	if err != nil {
		return Result{Err: err}
	}
	if cancelled(session) {
		return Result{Err: ErrCancelled}
	}
	previewFrames := make([]image.Image, 0, len(frames))
	for _, frame := range frames {
		if cancelled(session) {
			return Result{Err: ErrCancelled}
		}
		previewFrames = append(previewFrames, resizeFrame(frame, options))
	}
	var preview File
	if format == "apng" {
		preview, err = encodePNGAnimation(previewFrames, source, session)
	} else {
		preview, err = encodeFrames(previewFrames, delays, loops, format, false)
	}
	if err != nil {
		return Result{Err: err}
	}
	result := Result{Preview: preview}
	if options.Full {
		full := File{Content: source, MIME: formatMIME(format), Extension: "." + format, Width: frames[0].Bounds().Dx(), Height: frames[0].Bounds().Dy()}
		if format == "jpeg" {
			full.Extension = ".jpg"
		} else if format == "apng" {
			full.Extension = ".png"
		}
		if cancelled(session) {
			return Result{Err: ErrCancelled}
		}
		var optimized File
		var encodeErr error
		if format == "apng" {
			optimized, encodeErr = encodePNGAnimation(frames, source, session)
		} else {
			optimized, encodeErr = encodeFrames(frames, delays, loops, "webp", true)
		}
		if encodeErr != nil {
			return Result{Err: encodeErr}
		}
		if len(optimized.Content) < len(source) {
			full = optimized
		}
		result.Full = &full
	}
	return result
}

func formatMIME(format string) string {
	if format == "apng" {
		return "image/png"
	}
	if format == "ico" {
		return "image/vnd.microsoft.icon"
	}
	if format == "jpeg" {
		return "image/jpeg"
	}
	return "image/" + format
}

func decodeFrames(source []byte, session <-chan struct{}) ([]image.Image, []int, int, string, error) {
	if len(source) >= 6 && bytes.Equal(source[:4], []byte{0, 0, 1, 0}) {
		icon, iconErr := decodeIcon(source)
		if iconErr != nil {
			return nil, nil, 0, "", iconErr
		}
		return []image.Image{icon}, nil, 0, "ico", nil
	}
	configuration, format, err := image.DecodeConfig(bytes.NewReader(source))
	if err != nil {
		return nil, nil, 0, "", err
	}
	frameCount := 1
	if format == "gif" {
		frameCount, err = gifFrameCount(source)
	}
	if format == "webp" {
		frameCount, err = webpFrameCount(source)
	}
	if format == "png" {
		frameCount, err = pngFrameCount(source)
	}
	if err != nil {
		return nil, nil, 0, "", err
	}
	pixels := int64(configuration.Width) * int64(configuration.Height)
	bytesPerPixel := int64(8)
	if frameCount > 1 {
		bytesPerPixel = max(int64(frameCount)*5+8, int64(frameCount)*8)
	}
	metadataBytes := int64(frameCount) * 8192
	if pixels <= 0 || metadataBytes >= DecodedBudget || pixels > (DecodedBudget-metadataBytes)/bytesPerPixel {
		return nil, nil, 0, "", errors.New("decoded image exceeds preparation budget")
	}
	if format == "png" {
		chunks, chunkErr := pngChunks(source)
		if chunkErr != nil {
			return nil, nil, 0, "", chunkErr
		}
		animated := false
		for _, chunk := range chunks {
			if chunk.kind == "acTL" {
				animated = true
			}
		}
		if animated {
			frames, delays, plays, decodeErr := decodePNGAnimation(source, session)
			return frames, delays, plays, "apng", decodeErr
		}
	}
	if format == "webp" {
		animation, decodeErr := webp.DecodeAll(bytes.NewReader(source), webp.Options{AutoRotate: true, FrameSizeLimit: int(pixels), Threads: 1})
		if decodeErr != nil {
			return nil, nil, 0, "", decodeErr
		}
		return animation.Image, animation.Delay, animation.LoopCount, format, nil
	}
	if format == "gif" {
		animation, decodeErr := gif.DecodeAll(bytes.NewReader(source))
		if decodeErr != nil {
			return nil, nil, 0, "", decodeErr
		}
		frames := make([]image.Image, 0, len(animation.Image))
		canvas := image.NewNRGBA(image.Rect(0, 0, animation.Config.Width, animation.Config.Height))
		previous := image.NewNRGBA(canvas.Bounds())
		background := color.Color(color.Transparent)
		if palette, ok := animation.Config.ColorModel.(color.Palette); ok && int(animation.BackgroundIndex) < len(palette) {
			background = palette[animation.BackgroundIndex]
		}
		if len(animation.Image) > 0 && int(animation.BackgroundIndex) < len(animation.Image[0].Palette) {
			_, _, _, alpha := animation.Image[0].Palette[animation.BackgroundIndex].RGBA()
			if alpha == 0 {
				background = color.Transparent
			}
		}
		draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: background}, image.Point{}, draw.Src)
		for index, frame := range animation.Image {
			if cancelled(session) {
				return nil, nil, 0, "", ErrCancelled
			}
			draw.Draw(previous, previous.Bounds(), canvas, image.Point{}, draw.Src)
			draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
			completed := image.NewNRGBA(canvas.Bounds())
			draw.Draw(completed, completed.Bounds(), canvas, image.Point{}, draw.Src)
			frames = append(frames, completed)
			if index < len(animation.Disposal) {
				switch animation.Disposal[index] {
				case gif.DisposalBackground:
					draw.Draw(canvas, frame.Bounds(), &image.Uniform{C: background}, image.Point{}, draw.Src)
				case gif.DisposalPrevious:
					draw.Draw(canvas, canvas.Bounds(), previous, image.Point{}, draw.Src)
				}
			}
		}
		delays := make([]int, len(animation.Delay))
		for index, delay := range animation.Delay {
			delays[index] = delay * 10
		}
		// GIF -1 is a single play; WebP uses one to encode that behavior.
		loops := animation.LoopCount
		if loops < 0 {
			loops = 1
		} else if loops > 0 {
			loops++
		}
		return frames, delays, loops, format, nil
	}
	decoded, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		return nil, nil, 0, "", err
	}
	if format == "jpeg" {
		decoded = orientJPEG(decoded, source)
	}
	return []image.Image{decoded}, nil, 0, format, nil
}

func resizeFrame(source image.Image, options Options) image.Image {
	bounds := source.Bounds()
	sourceWidth, sourceHeight := float64(bounds.Dx()), float64(bounds.Dy())
	cropWidth, cropHeight := sourceWidth, sourceHeight
	if options.Fit == "cover" {
		ratio := float64(options.Width) / float64(options.Height)
		if sourceWidth/sourceHeight > ratio {
			cropWidth = sourceHeight * ratio
		} else {
			cropHeight = sourceWidth / ratio
		}
	}
	targetWidth, targetHeight := float64(options.Width*2), float64(options.Height*2)
	if options.Fit == "contain" {
		scale := math.Min(targetWidth/sourceWidth, targetHeight/sourceHeight)
		targetWidth, targetHeight = sourceWidth*scale, sourceHeight*scale
	}
	scale := math.Min(1, math.Min(cropWidth/targetWidth, cropHeight/targetHeight))
	width, height := max(1, int(math.Round(targetWidth*scale))), max(1, int(math.Round(targetHeight*scale)))
	left := bounds.Min.X + int(math.Round((sourceWidth-cropWidth)*options.PositionX))
	top := bounds.Min.Y + int(math.Round((sourceHeight-cropHeight)*options.PositionY))
	crop := image.Rect(left, top, min(bounds.Max.X, left+max(1, int(math.Round(cropWidth)))), min(bounds.Max.Y, top+max(1, int(math.Round(cropHeight)))))
	destination := image.NewNRGBA(image.Rect(0, 0, width, height))
	imagedraw.CatmullRom.Scale(destination, destination.Bounds(), source, crop, draw.Src, nil)
	return destination
}

func encodeFrames(frames []image.Image, delays []int, loops int, format string, lossless bool) (File, error) {
	if len(frames) == 0 {
		return File{}, errors.New("image has no frames")
	}
	var encoded bytes.Buffer
	var err error
	mime, extension := "image/png", ".png"
	if len(frames) > 1 || format == "webp" || format == "gif" {
		mime, extension = "image/webp", ".webp"
		encoding := webp.EncodeOptions{Quality: 90, Lossless: lossless, Method: 4, Threads: 1}
		if len(frames) > 1 {
			err = webp.EncodeAll(&encoded, &webp.WEBP{Image: frames, Delay: delays, LoopCount: loops}, encoding)
		} else {
			err = webp.Encode(&encoded, frames[0], encoding)
		}
	} else if format == "jpeg" {
		mime, extension = "image/jpeg", ".jpg"
		err = jpeg.Encode(&encoded, frames[0], &jpeg.Options{Quality: 90})
	} else {
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		err = encoder.Encode(&encoded, frames[0])
	}
	return File{Content: encoded.Bytes(), MIME: mime, Extension: extension, Width: frames[0].Bounds().Dx(), Height: frames[0].Bounds().Dy()}, err
}

// Count frames before decoding; compressed bytes alone cannot bound allocation.
func webpFrameCount(source []byte) (int, error) {
	if len(source) < 12 {
		return 0, errors.New("invalid WebP container")
	}
	count := 0
	for offset := 12; offset+8 <= len(source); {
		size := uint64(binary.LittleEndian.Uint32(source[offset+4:]))
		next := uint64(offset) + 8 + size + (size % 2)
		if next > uint64(len(source)) {
			return 0, errors.New("invalid WebP chunk")
		}
		if string(source[offset:offset+4]) == "ANMF" {
			count++
		}
		offset = int(next)
	}
	return max(1, count), nil
}

func gifFrameCount(source []byte) (int, error) {
	if len(source) < 13 {
		return 0, errors.New("invalid GIF header")
	}
	offset := 13
	if source[10]&128 != 0 {
		offset += 3 * (1 << ((source[10] & 7) + 1))
	}
	count := 0
	skipBlocks := func() bool {
		for offset < len(source) {
			size := int(source[offset])
			offset++
			if size == 0 {
				return true
			}
			offset += size
			if offset > len(source) {
				return false
			}
		}
		return false
	}
	for offset < len(source) {
		marker := source[offset]
		offset++
		switch marker {
		case 0x3b:
			if count == 0 {
				return 0, errors.New("GIF has no frames")
			}
			return count, nil
		case 0x21:
			offset++
			if !skipBlocks() {
				return 0, errors.New("invalid GIF extension")
			}
		case 0x2c:
			if offset+9 > len(source) {
				return 0, errors.New("invalid GIF frame")
			}
			flags := source[offset+8]
			offset += 9
			if flags&128 != 0 {
				offset += 3 * (1 << ((flags & 7) + 1))
			}
			offset++
			if !skipBlocks() {
				return 0, errors.New("invalid GIF pixels")
			}
			count++
		default:
			return 0, fmt.Errorf("invalid GIF marker %x", marker)
		}
	}
	return 0, errors.New("incomplete GIF")
}

func orientJPEG(source image.Image, encoded []byte) image.Image {
	orientation := jpegOrientation(encoded)
	if orientation <= 1 || orientation > 8 {
		return source
	}
	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	nextWidth, nextHeight := width, height
	if orientation >= 5 {
		nextWidth, nextHeight = height, width
	}
	rotated := image.NewNRGBA(image.Rect(0, 0, nextWidth, nextHeight))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			nextX, nextY := x, y
			switch orientation {
			case 2:
				nextX = width - 1 - x
			case 3:
				nextX, nextY = width-1-x, height-1-y
			case 4:
				nextY = height - 1 - y
			case 5:
				nextX, nextY = y, x
			case 6:
				nextX, nextY = height-1-y, x
			case 7:
				nextX, nextY = height-1-y, width-1-x
			case 8:
				nextX, nextY = y, width-1-x
			}
			rotated.Set(nextX, nextY, source.At(x+source.Bounds().Min.X, y+source.Bounds().Min.Y))
		}
	}
	return rotated
}

func jpegOrientation(encoded []byte) int {
	for offset := 2; offset+4 <= len(encoded) && encoded[offset] == 0xff; {
		marker := encoded[offset+1]
		if marker == 0xda || marker == 0xd9 {
			break
		}
		length := int(binary.BigEndian.Uint16(encoded[offset+2:]))
		if length < 2 || offset+2+length > len(encoded) {
			break
		}
		payload := encoded[offset+4 : offset+2+length]
		if marker == 0xe1 && bytes.HasPrefix(payload, []byte("Exif\x00\x00")) {
			tiff := payload[6:]
			if len(tiff) < 8 {
				return 1
			}
			var order binary.ByteOrder = binary.LittleEndian
			if string(tiff[:2]) == "MM" {
				order = binary.BigEndian
			} else if string(tiff[:2]) != "II" {
				return 1
			}
			start := uint64(order.Uint32(tiff[4:]))
			if start+2 > uint64(len(tiff)) {
				return 1
			}
			count := int(order.Uint16(tiff[start:]))
			for index := 0; index < count; index++ {
				entry := start + 2 + uint64(index)*12
				if entry+12 > uint64(len(tiff)) {
					return 1
				}
				if order.Uint16(tiff[entry:]) == 0x112 && order.Uint16(tiff[entry+2:]) == 3 && order.Uint32(tiff[entry+4:]) == 1 {
					return int(order.Uint16(tiff[entry+8:]))
				}
			}
		}
		offset += 2 + length
	}
	return 1
}
