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
	reason      string
	recovered   bool
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

func (catalog *Catalog) AcquirePrepared(decision Decision, pathname string, key HTMLVariantKey, reason ...*string) (*PreparedResponse, bool, error) {
	return catalog.acquireRAM(decision, pathname, key, false, reason...)
}

func (catalog *Catalog) AcquirePreparedFile(decision Decision, filename string, reason ...*string) (*PreparedResponse, bool, error) {
	return catalog.acquireRAM(decision, filename, HTMLVariantKey{}, true, reason...)
}

func (catalog *Catalog) acquireRAM(decision Decision, pathname string, key HTMLVariantKey, asset bool, reason ...*string) (*PreparedResponse, bool, error) {
	miss := func(cause string) {
		if len(reason) != 0 && reason[0] != nil {
			*reason[0] = cause
		}
	}
	if catalog == nil || catalog.runtime == nil || catalog.runtime.cache == nil {
		miss("disabled")
		return nil, false, nil
	}
	generation := decision.generation
	if !decision.Found || generation == nil || !generation.acquire() {
		miss("missing_generation")
		return nil, false, errors.New("public publication is unavailable")
	}
	if !catalog.runtime.admits(decision.Domain, generation) {
		generation.release()
		miss("stale_generation")
		return nil, false, errors.New("public publication changed before RAM delivery")
	}
	prepared := generation.ram[pathname]
	if asset {
		prepared = generation.assets[pathname]
	}
	if prepared != nil && !prepared.html {
		key = HTMLVariantKey{}
	}
	cause := ""
	switch {
	case prepared == nil:
		cause = generation.ramMisses[pathname]
		if cause == "" {
			cause = "no_prepared_object"
		}
	case prepared.resource == "" && decision.PageVersion != "" && prepared.version != decision.PageVersion:
		cause = "version_mismatch"
	case asset && prepared.resource != decision.File.Name || !asset && prepared.resource != decision.ResourceFile:
		cause = "resource_mismatch"
	case prepared.variants[key] == nil:
		variant, exists := prepared.prepared.variants[key]
		if !exists {
			cause = "variant"
		} else if prepared.prepared.info.Size()+int64(len(variant.bootstrap)) > MaximumRAMRepresentationBytes {
			cause = "too_large"
		} else {
			cause = prepared.reason
		}
	}
	if cause != "" {
		miss(cause)
		generation.release()
		return nil, false, nil
	}
	return &PreparedResponse{contentType: prepared.contentType, html: prepared.html, generation: generation, body: prepared.variants[key], prepared: prepared.prepared, etag: prepared.prepared.variants[key].etag}, true, nil
}

func (response *PreparedResponse) HTML() bool  { return response.html }
func (response *PreparedResponse) Size() int64 { return int64(len(response.body.bytes)) }

func (runtime *PublicRuntime) retainRAMBody(generation *publicGeneration, identity string, size int64, stop <-chan struct{}, fill func([]byte) error) (*ramBody, error) {
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
		if err := fill(payload); err != nil {
			return nil, err
		}
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
		generation.ramMisses[pathname] = "too_large"
		return nil, nil
	}
	var prefix [512]byte
	count, readErr := file.ReadAt(prefix[:], 0)
	if readErr != nil && readErr != io.EOF {
		return nil, readErr
	}
	contentType := mime.TypeByExtension(filepath.Ext(pathname))
	if runtime.options.ContentType != nil {
		contentType = runtime.options.ContentType(pathname, string(prefix[:count]))
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
		contentType = http.DetectContentType(prefix[:count])
	}
	recovered := version == ""
	if recovered {
		if policy.Frozen || policy.Versioned {
			generation.ramMisses[pathname] = "missing_version"
			return nil, nil
		}
		version, err = fingerprintRAMSource(file, info, stop)
		if err != nil {
			return nil, err
		}
		if resource == "" {
			generation.recoveredPages++
		} else {
			generation.recoveredFiles++
		}
	}
	key := HTMLVariantKey{}
	prepared := &PreparedHTML{info: info, modified: info.ModTime(), lastModified: info.ModTime().UTC().Format(http.TimeFormat), variants: map[HTMLVariantKey]htmlVariant{key: {etag: `"` + version + `"`}}}
	result := &preparedRAMFile{contentType: contentType, resource: resource, prepared: prepared, version: version, variants: make(map[HTMLVariantKey]*ramBody), reason: "budget", recovered: recovered}
	directory, relative := runtime.ramSource(policy, pathname, resource)
	generation.pendingRAM = append(generation.pendingRAM, ramCandidate{generation: generation, response: result, pathname: pathname, domain: policy.Domain, directory: directory, relative: relative, size: info.Size(), version: version, identity: policy.Domain + "\x00raw\x00" + version})
	if strings.Contains(contentType, "text/css") {
		generation.references[pathname] = readRAMReferences(file, false, stop)
	}
	return result, nil
}

func (runtime *PublicRuntime) prepareRAMStatic(generation *publicGeneration, policy Policy, pathname string, file *os.File, stop <-chan struct{}) error {
	prepared, err := runtime.prepareRAMPlain(generation, policy, pathname, ramPageVersion(policy, pathname), "", file, stop)
	if prepared != nil {
		generation.ram[pathname] = prepared
	}
	return err
}

func (runtime *PublicRuntime) prepareRAMAssets(generation *publicGeneration, policy Policy, stop <-chan struct{}) error {
	if runtime.ramPolicyReason(policy) != "" {
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
	publicNames := make(map[string]bool)
	for name := range versions {
		publicNames[name] = true
	}
	if !policy.Frozen && !policy.Versioned {
		for _, name := range policy.ResourcePaths {
			publicNames[name] = true
		}
		for _, rule := range policy.Files {
			publicNames[rule.Name] = true
		}
	}
	for name := range publicNames {
		names = append(names, name)
	}
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
		if MissingSource(err) {
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
			generation.references[pathname] = generation.references[name]
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

// Administrator IP rules establish trust for the control plane. They do not
// make an otherwise public page private or change its anonymous representation.
func (runtime *PublicRuntime) ramPolicyReason(policy Policy) string {
	if runtime.cache == nil {
		return "disabled"
	}
	if policy.RoutingVersion != 3 || policy.PrivateTestDrive || policy.PendingRegistration || policy.Stealth {
		return "restricted"
	}
	return ""
}

func (runtime *PublicRuntime) ramPageReason(policy Policy, pathname string) string {
	if reason := runtime.ramPolicyReason(policy); reason != "" {
		return reason
	}
	if hiddenVersionPath(policy.Hidden, "page", pathname) {
		return "restricted"
	}
	for _, password := range policy.Passwords {
		if dirprotect.HasProtectedPrefix(pathname, password.Path) {
			return "restricted"
		}
	}
	if (policy.Frozen || policy.Versioned) && ramPageVersion(policy, pathname) == "" {
		return "missing_version"
	}
	return ""
}

func (runtime *PublicRuntime) ramEligible(policy Policy, pathname string) bool {
	return runtime.ramPageReason(policy, pathname) == ""
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
	if reason := runtime.ramPageReason(policy, pathname); reason != "" {
		generation.ramMisses[pathname] = reason
		return nil
	}
	generation.pageSources[pathname] = runtime.options.StaticPath(pathname)
	generation.references[pathname] = readRAMReferences(file, true, stop)
	if prepared.info.Size() > MaximumRAMRepresentationBytes {
		generation.ramMisses[pathname] = "too_large"
		return nil
	}
	version := ramPageVersion(policy, pathname)
	recovered := version == ""
	if recovered {
		// Legacy ordinary policies already authorize this live published file. Bind
		// its existing preparation digest to this generation, never to an HTTP read.
		version = prepared.sourceVersion
		generation.recoveredPages++
	}
	if prepared.sourceVersion != version {
		return errors.New("published HTML version changed during RAM preparation")
	}
	result := &preparedRAMFile{html: true, contentType: "text/html; charset=utf-8", prepared: prepared, version: version, variants: make(map[HTMLVariantKey]*ramBody), reason: "budget", recovered: recovered}
	generation.ram[pathname] = result
	directory, relative := runtime.ramSource(policy, pathname, "")
	for key, variant := range prepared.variants {
		size := prepared.info.Size() + int64(len(variant.bootstrap))
		if size > MaximumRAMRepresentationBytes {
			continue
		}
		generation.pendingRAM = append(generation.pendingRAM, ramCandidate{generation: generation, response: result, pathname: pathname, domain: policy.Domain, directory: directory, relative: relative, key: key, size: size, version: version, identity: policy.Domain + "\x00" + variant.etag, bootstrap: variant.bootstrap, insertion: prepared.insertion})
	}
	return nil
}

// Only bounded ordinary files can acquire a missing fingerprint. Existing
// fingerprints and checkpoint versions remain authoritative and are never healed.
func fingerprintRAMSource(file *os.File, before os.FileInfo, stop <-chan struct{}) (string, error) {
	digest := sha256.New()
	count, err := io.Copy(digest, preparationReader{source: io.NewSectionReader(file, 0, before.Size()+1), stop: stop})
	if err != nil {
		return "", err
	}
	after, err := file.Stat()
	if err != nil {
		return "", err
	}
	if count != before.Size() || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return "", errors.New("public source changed during fingerprint preparation")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
