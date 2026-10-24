package publicsite

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strconv"

	"golang.org/x/net/html"
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
		prefix := html.buffer[:length]
		if _, err := html.source.ReadAt(prefix, 0); err != nil && err != io.EOF {
			return 0, err
		}
		html.insertion = int64(bootstrapPosition(prefix))
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

// Preserve the doctype and start the asynchronous visitor contact before page
// scripts. A bounded prefix is sufficient, including documents with implicit heads.
func bootstrapPosition(prefix []byte) int {
	offset, insertion := 0, 0
	if bytes.HasPrefix(prefix, []byte{0xef, 0xbb, 0xbf}) {
		offset, insertion = 3, 3
		prefix = prefix[3:]
	}
	tokens := html.NewTokenizer(bytes.NewReader(prefix))
	for {
		kind := tokens.Next()
		offset += len(tokens.Raw())
		switch kind {
		case html.DoctypeToken:
			insertion = offset
		case html.CommentToken:
		case html.TextToken:
			if len(bytes.TrimSpace(tokens.Raw())) > 0 {
				return insertion
			}
		case html.StartTagToken:
			name, _ := tokens.TagName()
			if string(name) == "head" {
				return offset
			}
			if string(name) != "html" {
				return insertion
			}
			insertion = offset
		default:
			return insertion
		}
	}
}
