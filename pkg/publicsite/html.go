package publicsite

import (
	"io"
	"net/http"
	"os"
	"strconv"
)

// HTML inserts a small service bootstrap without retaining a whole page or
// waiting for a rendering worker. Each request owns its file and reader.
type Streamer struct{ buffers chan []byte }

func NewStreamer() *Streamer {
	streamer := &Streamer{buffers: make(chan []byte, 32)}
	for index := 0; index < cap(streamer.buffers); index++ {
		streamer.buffers <- make([]byte, 32<<10)
	}
	return streamer
}
func HTML(w http.ResponseWriter, r *http.Request, file *os.File, bootstrap string, variant string) error {
	return (*Streamer)(nil).HTML(w, r, file, bootstrap, variant)
}
func (streamer *Streamer) HTML(w http.ResponseWriter, r *http.Request, file *os.File, bootstrap string, variant string, redirect ...string) error {
	var buffer []byte
	if streamer != nil {
		select {
		case buffer = <-streamer.buffers:
		default:
		}
	}
	if buffer == nil {
		buffer = make([]byte, 8<<10)
	}
	if streamer != nil {
		defer func() {
			select {
			case streamer.buffers <- buffer:
			default:
			}
		}()
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.ErrInvalid
	}
	if len(redirect) > 0 && redirect[0] != "" {
		http.Redirect(w, r, redirect[0], http.StatusMovedPermanently)
		return nil
	}
	size := info.Size()
	representation := insertedHTML{source: file, insertion: -1, size: size, bootstrap: bootstrap, buffer: buffer}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("ETag", `W/"`+strconv.FormatInt(info.ModTime().UnixNano(), 16)+"-"+strconv.FormatInt(size, 16)+"-"+variant+`"`)
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(streamWriter{ResponseWriter: w, buffer: buffer}, r, info.Name(), info.ModTime(), io.NewSectionReader(&representation, 0, size+int64(len(bootstrap))))
	return nil
}

// A buffer travels with a request and returns only after the last write. This
// avoids a shared pool and keeps slow readers independent of other visitors.
type streamWriter struct {
	http.ResponseWriter
	buffer []byte
}

func (writer streamWriter) ReadFrom(source io.Reader) (int64, error) {
	return io.CopyBuffer(writer.ResponseWriter, source, writer.buffer)
}

type insertedHTML struct {
	source          io.ReaderAt
	insertion, size int64
	bootstrap       string
	buffer          []byte
}

func (html *insertedHTML) ReadAt(destination []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, os.ErrInvalid
	}
	if html.insertion < 0 {
		length := min(html.size, 8192)
		tail := html.buffer[:length]
		if _, err := html.source.ReadAt(tail, html.size-length); err != nil && err != io.EOF {
			return 0, err
		}
		html.insertion = html.size
		if closing := lastClosingBody(tail); closing >= 0 {
			html.insertion = html.size - length + int64(closing)
		}
	}

	total := 0
	bootstrapEnd := html.insertion + int64(len(html.bootstrap))
	for len(destination) > 0 {
		var count int
		var err error
		switch {
		case offset < html.insertion:
			length := min(int64(len(destination)), html.insertion-offset)
			count, err = html.source.ReadAt(destination[:length], offset)
		case offset < bootstrapEnd:
			count = copy(destination, html.bootstrap[offset-html.insertion:])
		case offset < html.size+int64(len(html.bootstrap)):
			count, err = html.source.ReadAt(destination, offset-int64(len(html.bootstrap)))
		default:
			return total, io.EOF
		}
		total += count
		destination = destination[count:]
		offset += int64(count)
		if err != nil {
			return total, err
		}
		if count == 0 {
			return total, io.ErrNoProgress
		}
	}
	return total, nil
}

func lastClosingBody(tail []byte) int {
	for offset := len(tail) - 7; offset >= 0; offset-- {
		marker := "</body>"
		matched := true
		for index := 0; index < len(marker); index++ {
			character := tail[offset+index]
			if character >= 'A' && character <= 'Z' {
				character += 'a' - 'A'
			}
			if character != marker[index] {
				matched = false
				break
			}
		}
		if matched {
			return offset
		}
	}
	return -1
}
