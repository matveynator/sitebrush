package publicsite

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/matveynator/sitebrush/v2/pkg/dirprotect"
)

type preparedRAMFile struct {
	contentType string
	resource    string
	html        bool
	prepared    *PreparedHTML
	version     string
	variants    map[HTMLVariantKey]*ramBody
}

// A response borrows its generation until the HTTP writer has consumed the body.
// Its payload remains private to this package so callers cannot alter shared bytes.
type PreparedResponse struct {
	contentType string
	html        bool
	generation  *publicGeneration
	body        *ramBody
	prepared    *PreparedHTML
	etag        string
}

func (response *PreparedResponse) Close() {
	if response.generation != nil {
		response.generation.release()
		response.generation = nil
	}
}

func (catalog *Catalog) AcquirePrepared(decision Decision, pathname string, key HTMLVariantKey) (*PreparedResponse, bool, error) {
	return catalog.acquireRAM(decision, pathname, key, false)
}

func (catalog *Catalog) AcquirePreparedFile(decision Decision, filename string) (*PreparedResponse, bool, error) {
	return catalog.acquireRAM(decision, filename, HTMLVariantKey{}, true)
}

func (catalog *Catalog) acquireRAM(decision Decision, pathname string, key HTMLVariantKey, asset bool) (*PreparedResponse, bool, error) {
	if catalog == nil || catalog.runtime == nil || catalog.runtime.cache == nil {
		return nil, false, nil
	}
	generation := decision.generation
	if !decision.Found || generation == nil || !generation.acquire() {
		return nil, false, errors.New("public publication is unavailable")
	}
	if !catalog.runtime.admits(decision.Domain, generation) {
		generation.release()
		return nil, false, errors.New("public publication changed before RAM delivery")
	}
	prepared := generation.ram[pathname]
	if asset {
		prepared = generation.assets[pathname]
	}
	if prepared != nil && !prepared.html {
		key = HTMLVariantKey{}
	}
	if prepared == nil || prepared.resource == "" && decision.PageVersion != "" && prepared.version != decision.PageVersion || prepared.resource != "" && prepared.resource != decision.ResourceFile && (!asset || prepared.resource != decision.File.Name) || prepared.variants[key] == nil {
		generation.release()
		return nil, false, nil
	}
	return &PreparedResponse{contentType: prepared.contentType, html: prepared.html, generation: generation, body: prepared.variants[key], prepared: prepared.prepared, etag: prepared.prepared.variants[key].etag}, true, nil
}

func (response *PreparedResponse) HTML() bool  { return response.html }
func (response *PreparedResponse) Size() int64 { return int64(len(response.body.bytes)) }

func (runtime *PublicRuntime) retainRAMBody(generation *publicGeneration, identity string, size int64, stop <-chan struct{}, fill func([]byte)) (*ramBody, error) {
	if body := generation.ramBodies[identity]; body != nil {
		return body, nil
	}
	reservation, err := runtime.cache.exchange(ramJob{action: "reserve", key: identity, size: size}, stop)
	if err != nil || reservation.ticket == 0 {
		return nil, err
	}
	generation.ramLeases = append(generation.ramLeases, reservation.ticket)
	body := reservation.body
	if body == nil {
		payload := make([]byte, size)
		fill(payload)
		committed, commitErr := runtime.cache.exchange(ramJob{action: "commit", ticket: reservation.ticket, body: &ramBody{bytes: payload, length: strconv.Itoa(len(payload))}}, stop)
		if commitErr != nil {
			return nil, commitErr
		}
		body = committed.body
	}
	if body != nil {
		generation.ramBodies[identity] = body
	}
	return body, nil
}

func (runtime *PublicRuntime) prepareRAMPlain(generation *publicGeneration, policy Policy, pathname, version, resource string, file *os.File, stop <-chan struct{}) (*preparedRAMFile, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaximumRAMRepresentationBytes {
		return nil, nil
	}
	source := make([]byte, info.Size())
	if _, err = io.ReadFull(io.NewSectionReader(file, 0, info.Size()), source); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(source)
	if hex.EncodeToString(digest[:]) != version {
		return nil, errors.New("public resource version changed during RAM preparation")
	}
	contentType := mime.TypeByExtension(filepath.Ext(pathname))
	if runtime.options.ContentType != nil {
		contentType = runtime.options.ContentType(pathname, string(source[:min(512, len(source))]))
	}
	if resource != "" {
		if override := policy.ResourceTypes[pathname]; override != "" {
			contentType = override
		}
		// Raw HTML downloads may require the existing debt or registration overlays.
		if strings.HasPrefix(strings.ToLower(contentType), "text/html") {
			return nil, nil
		}
	}
	if contentType == "" {
		contentType = http.DetectContentType(source[:min(512, len(source))])
	}
	body, err := runtime.retainRAMBody(generation, policy.Domain+"\x00raw\x00"+version, int64(len(source)), stop, func(destination []byte) { copy(destination, source) })
	if err != nil || body == nil {
		return nil, err
	}
	key := HTMLVariantKey{}
	prepared := &PreparedHTML{info: info, modified: info.ModTime(), lastModified: info.ModTime().UTC().Format(http.TimeFormat), variants: map[HTMLVariantKey]htmlVariant{key: {etag: `"` + version + `"`}}}
	return &preparedRAMFile{contentType: contentType, resource: resource, prepared: prepared, version: version, variants: map[HTMLVariantKey]*ramBody{key: body}}, nil
}

func (runtime *PublicRuntime) prepareRAMStatic(generation *publicGeneration, policy Policy, pathname string, file *os.File, stop <-chan struct{}) error {
	prepared, err := runtime.prepareRAMPlain(generation, policy, pathname, ramPageVersion(policy, pathname), "", file, stop)
	if prepared != nil {
		generation.ram[pathname] = prepared
	}
	return err
}

func (runtime *PublicRuntime) prepareRAMAssets(generation *publicGeneration, policy Policy, stop <-chan struct{}) error {
	if runtime.cache == nil || policy.PrivateTestDrive || policy.PendingRegistration || policy.Stealth || len(policy.AllowedIPs) != 0 {
		return nil
	}
	directory := "files"
	versions := policy.PreparedFiles
	if policy.Frozen {
		directory = "versions"
		versions = policy.FileVersions
	}
	root := generation.roots[directory]
	if root == nil {
		return nil
	}
	names := make([]string, 0, len(versions))
	for name := range versions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		select {
		case <-stop:
			return errors.New("RAM preparation stopped")
		default:
		}
		if hiddenVersionPath(policy.Hidden, "files", name) {
			continue
		}
		public := true
		for _, rule := range policy.Files {
			if rule.Name == name && admittedFileRule(rule).Mode != "" && admittedFileRule(rule).Mode != "public" {
				public = false
			}
		}
		if !public {
			continue
		}
		relative := name
		if directory == "versions" {
			relative = versions[name]
		}
		file, err := root.Open(relative)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		prepared, err := runtime.prepareRAMPlain(generation, policy, name, versions[name], name, file, stop)
		file.Close()
		if err != nil {
			return err
		}
		if prepared == nil {
			continue
		}
		generation.assets[name] = prepared
		for pathname, resource := range policy.ResourcePaths {
			if resource != name {
				continue
			}
			allowed := true
			for _, rule := range policy.Passwords {
				if dirprotect.HasProtectedPrefix(pathname, rule.Path) {
					allowed = false
				}
			}
			if !allowed {
				continue
			}
			alias := *prepared
			if contentType := policy.ResourceTypes[pathname]; contentType != "" {
				alias.contentType = contentType
			}
			if strings.HasPrefix(strings.ToLower(alias.contentType), "text/html") {
				continue
			}
			generation.ram[pathname] = &alias
		}
	}
	return nil
}

func (response *PreparedResponse) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", response.contentType)
	w.Header().Set("ETag", response.etag)
	if response.html && w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if w.Header().Get("Content-Encoding") != "" {
		http.ServeContent(w, r, response.prepared.info.Name(), response.prepared.modified, bytes.NewReader(response.body.bytes))
		return nil
	}
	for _, header := range []string{"Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "If-Range"} {
		if _, present := r.Header[header]; present {
			http.ServeContent(w, r, response.prepared.info.Name(), response.prepared.modified, bytes.NewReader(response.body.bytes))
			return nil
		}
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Last-Modified", response.prepared.lastModified)
	w.Header().Set("Content-Length", response.body.length)
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return nil
	}
	_, err := w.Write(response.body.bytes)
	return err
}

func (runtime *PublicRuntime) ramEligible(policy Policy, pathname string) bool {
	if runtime.cache == nil || policy.PrivateTestDrive || policy.PendingRegistration || policy.Stealth || len(policy.AllowedIPs) != 0 || ramPageVersion(policy, pathname) == "" || hiddenVersionPath(policy.Hidden, "page", pathname) {
		return false
	}
	for _, password := range policy.Passwords {
		if dirprotect.HasProtectedPrefix(pathname, password.Path) {
			return false
		}
	}
	return true
}

func ramPageVersion(policy Policy, pathname string) string {
	if version := policy.PublishedVersions[pathname]; version != "" {
		return version
	}
	if policy.Frozen {
		return ""
	}
	return policy.PreparedPages[pathname]
}

func (runtime *PublicRuntime) prepareRAM(generation *publicGeneration, policy Policy, pathname string, file *os.File, prepared *PreparedHTML, stop <-chan struct{}) error {
	if !runtime.ramEligible(policy, pathname) || prepared.info.Size() > MaximumRAMRepresentationBytes {
		return nil
	}
	version := ramPageVersion(policy, pathname)
	source := make([]byte, prepared.info.Size())
	if _, err := io.ReadFull(io.NewSectionReader(file, 0, int64(len(source))), source); err != nil {
		return err
	}
	digest := sha256.Sum256(source)
	if hex.EncodeToString(digest[:]) != version || prepared.sourceVersion != version {
		return errors.New("published HTML version changed during RAM preparation")
	}
	variants := make(map[HTMLVariantKey]*ramBody)
	keys := make([]HTMLVariantKey, 0, len(prepared.variants))
	for key := range prepared.variants {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(first, second int) bool {
		left, right := keys[first], keys[second]
		if left.Language != right.Language {
			return left.Language < right.Language
		}
		if left.Menu != right.Menu {
			return left.Menu
		}
		return !left.Updating && right.Updating
	})
	for _, key := range keys {
		select {
		case <-stop:
			return errors.New("RAM preparation stopped")
		default:
		}
		variant := prepared.variants[key]
		size := int64(len(source) + len(variant.bootstrap))
		if size > MaximumRAMRepresentationBytes {
			continue
		}
		identity := policy.Domain + "\x00" + variant.etag

		body, err := runtime.retainRAMBody(generation, identity, size, stop, func(payload []byte) {
			position := int(prepared.insertion)
			copy(payload, source[:position])
			copy(payload[position:], variant.bootstrap)
			copy(payload[position+len(variant.bootstrap):], source[position:])
		})
		if err != nil {
			return err
		}
		if body != nil {
			variants[key] = body
		}

	}
	if len(variants) != 0 {
		generation.ram[pathname] = &preparedRAMFile{html: true, contentType: "text/html; charset=utf-8", prepared: prepared, version: version, variants: variants}
	}
	return nil
}
