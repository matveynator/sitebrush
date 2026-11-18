package publicsite

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"time"
)

type HTMLVariantKey struct {
	Language string
	Menu     bool
	Updating bool
}

type htmlVariant struct {
	bootstrap string
	etag      string
}

// Preparation belongs to a publication generation, never to a visitor. File
// identity prevents using offsets from a replaced file with equal timestamps.
type PreparedHTML struct {
	info          os.FileInfo
	modified      time.Time
	lastModified  string
	sourceVersion string
	insertion     int64
	variants      map[HTMLVariantKey]htmlVariant
}

func (runtime *PublicRuntime) prepareHTML(generation *publicGeneration, policy Policy, stop <-chan struct{}) error {
	if runtime.options.PrepareBootstrap == nil || runtime.options.StaticPath == nil {
		return errors.New("public HTML preparation is not configured")
	}
	paths := make(map[string]int64)
	for pathname := range policy.Layouts {
		paths[pathname] = 0
	}
	for pathname := range policy.PublishedVersions {
		paths[pathname] = 0
	}
	for pathname := range policy.PreparedPages {
		paths[pathname] = 0
	}
	ordered := make([]string, 0, len(paths))
	for pathname := range paths {
		directory, relative := "static", runtime.options.StaticPath(pathname)
		if version := policy.PublishedVersions[pathname]; version != "" {
			directory, relative = "versions", version
		}
		if root := generation.roots[directory]; root != nil {
			if info, err := root.Stat(relative); err == nil {
				paths[pathname] = info.Size()
			}
		}
		ordered = append(ordered, pathname)
	}
	// Admission is deterministic and favors the landing page and smaller pages.
	sort.Slice(ordered, func(first, second int) bool {
		left, right := ordered[first], ordered[second]
		if left == "/" || right == "/" {
			return left == "/"
		}
		if paths[left] != paths[right] {
			return paths[left] < paths[right]
		}
		return left < right
	})
	for _, pathname := range ordered {
		if err := runtime.prepareHTMLPath(generation, policy, pathname, stop); err != nil {
			return err
		}
	}
	root := generation.roots["static"]
	if root == nil {
		return nil
	}
	// Layouts are optional analytics metadata in older policies. Published HTML
	// must remain independent of both that index and the administrative database.
	return fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		select {
		case <-stop:
			return errors.New("public HTML preparation stopped")
		default:
		}
		if walkErr != nil {
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() && entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		// Optional legacy entries that cannot be opened remain unavailable;
		// they cannot prevent unrelated published pages from being prepared.
		if err := runtime.prepareHTMLPath(generation, policy, "/"+relative, stop); err != nil {
			select {
			case <-stop:
				return err
			default:
				return nil
			}
		}
		return nil
	})
}

func (runtime *PublicRuntime) prepareHTMLPath(generation *publicGeneration, policy Policy, pathname string, stop <-chan struct{}) error {
	select {
	case <-stop:
		return errors.New("public HTML preparation stopped")
	default:
	}
	directory, relative := "static", runtime.options.StaticPath(pathname)
	if version := policy.PublishedVersions[pathname]; version != "" {
		directory, relative = "versions", version
	}
	key := directory + "\x00" + relative
	if generation.html[key] != nil && (!runtime.ramEligible(policy, pathname) || generation.ram[pathname] != nil) {
		return nil
	}
	root := generation.roots[directory]
	if root == nil {
		return nil
	}
	info, err := root.Stat(relative)
	if MissingSource(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	file, err := root.Open(relative)
	if MissingSource(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if runtime.options.HTMLFile != nil {
		var prefix [512]byte
		count, readErr := file.ReadAt(prefix[:], 0)
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		if !runtime.options.HTMLFile(pathname, string(prefix[:count])) {
			if runtime.ramEligible(policy, pathname) {
				return runtime.prepareRAMStatic(generation, policy, pathname, file, stop)
			}
			return nil
		}
	}
	prepared, err := prepareHTMLFile(file, runtime.options.PrepareBootstrap(policy, pathname), stop)
	if err != nil {
		return err
	}
	if generation.html[key] == nil {
		generation.html[key] = prepared
	}
	return runtime.prepareRAM(generation, policy, pathname, file, prepared, stop)
}

func prepareHTMLFile(file *os.File, bootstraps map[HTMLVariantKey]string, stop <-chan struct{}) (*PreparedHTML, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, os.ErrInvalid
	}
	prefix := make([]byte, min(info.Size(), 8192))
	if _, err = file.ReadAt(prefix, 0); err != nil && err != io.EOF {
		return nil, err
	}
	digest := sha256.New()
	if _, err = io.Copy(digest, preparationReader{source: file, stop: stop}); err != nil {
		return nil, err
	}
	current, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
		return nil, errors.New("published HTML changed during preparation")
	}
	sourceDigest := digest.Sum(nil)
	prepared := &PreparedHTML{info: info, modified: time.Now(), insertion: int64(bootstrapPosition(prefix)), variants: make(map[HTMLVariantKey]htmlVariant, len(bootstraps))}
	prepared.sourceVersion = hex.EncodeToString(sourceDigest)
	prepared.lastModified = prepared.modified.UTC().Format(http.TimeFormat)
	for key, bootstrap := range bootstraps {
		fingerprint := sha256.New()
		fingerprint.Write(sourceDigest)
		io.WriteString(fingerprint, bootstrap)
		prepared.variants[key] = htmlVariant{bootstrap: bootstrap, etag: `"` + hex.EncodeToString(fingerprint.Sum(nil)) + `"`}
	}
	return prepared, nil
}

type preparationReader struct {
	source io.Reader
	stop   <-chan struct{}
}

func (reader preparationReader) Read(destination []byte) (int, error) {
	select {
	case <-reader.stop:
		return 0, errors.New("public HTML preparation stopped")
	default:
		return reader.source.Read(destination)
	}
}

func (streamer *Streamer) Prepared(w http.ResponseWriter, r *http.Request, file *os.File, prepared *PreparedHTML, key HTMLVariantKey, redirect string) error {
	if prepared == nil {
		return errors.New("published HTML is not prepared")
	}
	variant, found := prepared.variants[key]
	if !found {
		return errors.New("published HTML variant is unavailable")
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, prepared.info) || info.Size() != prepared.info.Size() || !info.ModTime().Equal(prepared.info.ModTime()) {
		return errors.New("published HTML preparation is stale")
	}
	if redirect != "" {
		http.Redirect(w, r, redirect, http.StatusMovedPermanently)
		return nil
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("ETag", variant.etag)
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	// Independent section readers retain Range/Seek semantics without scanning
	// HTML or copying a whole representation for each visitor.
	content := &preparedHTMLReader{source: file, insertion: prepared.insertion, size: info.Size(), bootstrap: variant.bootstrap}
	http.ServeContent(w, r, info.Name(), prepared.modified, io.NewSectionReader(content, 0, info.Size()+int64(len(variant.bootstrap))))
	return nil
}

type preparedHTMLReader struct {
	source          io.ReaderAt
	insertion, size int64
	bootstrap       string
}

func (reader *preparedHTMLReader) ReadAt(destination []byte, offset int64) (int, error) {
	// The existing composition implements seekable byte ranges; a prepared
	// insertion bypasses its tokenizer and buffer acquisition entirely.
	representation := insertedHTML{source: reader.source, insertion: reader.insertion, size: reader.size, bootstrap: reader.bootstrap}
	return representation.ReadAt(destination, offset)
}
