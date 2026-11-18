package publicsite

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/matveynator/sitebrush/v2/pkg/sitefreeze"
)

// RuntimeOptions are fixed before readers start. Only public snapshots and
// their resource lifetimes use shared atomics; control state stays channel-owned.
type RuntimeOptions struct {
	Mode             StaticMode
	Root             string
	StorageName      func(string) string
	StaticPath       func(string) string
	HTMLFile         func(string, string) bool
	PrepareBootstrap func(Policy, string) map[HTMLVariantKey]string
	RAMCacheBytes    int64
	ContentType      func(string, string) string
	DefaultLanguage  string
}

type PublicRuntime struct {
	current atomic.Pointer[PublicSnapshot]
	options RuntimeOptions
	cache   *ramCache
}

// Published maps and their entries are never mutated. A request's reference to
// this snapshot also keeps policy data alive across administrative changes.
type PublicSnapshot struct {
	policies map[string]cachedPolicy
	blocked  map[string]bool
	trust    serverTrustCache
	memory   bool
	roots    map[string]*publicGeneration
}

func (snapshot *PublicSnapshot) lookup(query Lookup) Decision {
	cached, found := snapshot.policies[query.Host]
	if !found {
		return Decision{}
	}
	if cached.policy.Domain != query.Host {
		cached, found = snapshot.policies[cached.policy.Domain]
		admitted := false
		for _, alias := range cached.policy.Aliases {
			if alias == query.Host {
				admitted = true
				break
			}
		}
		if !found || !admitted {
			return Decision{}
		}
	}
	if cached.policy.RoutingVersion != 3 || snapshot.blocked[cached.policy.Domain] {
		return Decision{Known: true, Domain: cached.policy.Domain}
	}
	decision := decidePolicy(query, cached, snapshot.trust, snapshot.blocked[snapshot.trust.domain])
	decision.generation = snapshot.roots[decision.Domain]
	return decision
}

const retiredGeneration uint64 = 1 << 63

type publicGeneration struct {
	pendingRAM     []ramCandidate
	references     map[string][]string
	pageSources    map[string]string
	ramMisses      map[string]string
	recoveredPages int
	recoveredFiles int
	roots          map[string]*os.Root
	html           map[string]*PreparedHTML
	ram            map[string]*preparedRAMFile
	assets         map[string]*preparedRAMFile
	ramBodies      map[string]*ramBody
	ramLeases      []uint64
	cache          *ramCache
	readers        atomic.Uint64
	released       chan struct{}
}

// PublishedFile pins its roots until Close. Generic OpenFile callers need the
// root only through Open: the resulting os.File owns an independent descriptor.
type PublishedFile struct {
	File       *os.File
	HTML       *PreparedHTML
	generation *publicGeneration
}

func (file *PublishedFile) Close() error {
	err := file.File.Close()
	if file.generation != nil {
		file.generation.release()
		file.generation = nil
	}
	return err
}

func (generation *publicGeneration) acquire() bool {
	for {
		readers := generation.readers.Load()
		if readers&retiredGeneration != 0 {
			return false
		}
		if generation.readers.CompareAndSwap(readers, readers+1) {
			return true
		}
	}
}

func (generation *publicGeneration) release() {
	if generation.readers.Add(^uint64(0)) == retiredGeneration {
		close(generation.released)
	}
}

func (generation *publicGeneration) retire() {
	if generation.readers.Add(retiredGeneration) == retiredGeneration {
		close(generation.released)
	}
	// Slow visitors retain only their own generation. Descriptor closure never
	// runs on the response path and never waits in the publication owner's loop.
	go func() {
		<-generation.released
		generation.cache.release(generation.ramLeases)
		for _, root := range generation.roots {
			root.Close()
		}
	}()
}

func retireGenerations(generations map[string]*publicGeneration) {
	for _, generation := range generations {
		generation.retire()
	}
}

func (runtime *PublicRuntime) close() {
	if previous := runtime.current.Swap(nil); previous != nil {
		retireGenerations(previous.roots)
	}
}

func (runtime *PublicRuntime) publish(policies map[string]cachedPolicy, blocked map[string]bool, trust serverTrustCache, memory bool, update catalogUpdate) {
	next := &PublicSnapshot{memory: memory, policies: make(map[string]cachedPolicy, len(policies)), blocked: make(map[string]bool, len(blocked)), roots: make(map[string]*publicGeneration)}
	for domain, policy := range policies {
		next.policies[domain] = policy
	}
	for domain, unavailable := range blocked {
		next.blocked[domain] = unavailable
	}
	next.trust = trust
	next.trust.rules = append([]TrustedIP(nil), trust.rules...)
	previous := runtime.current.Load()
	if previous != nil {
		for domain, generation := range previous.roots {
			next.roots[domain] = generation
		}
	}
	if update.prepared != nil {
		if update.memory != nil && *update.memory {
			next.roots = make(map[string]*publicGeneration, len(update.prepared))
		} else {
			delete(next.roots, update.domain)
		}
		for domain, generation := range update.prepared {
			next.roots[domain] = generation
		}
	}
	runtime.current.Store(next)
	if previous != nil {
		for domain, generation := range previous.roots {
			if next.roots[domain] != generation {
				generation.retire()
			}
		}
	}
}

func (runtime *PublicRuntime) acquire(domain string) (*publicGeneration, error) {
	for {
		snapshot := runtime.current.Load()
		if snapshot == nil {
			return nil, errors.New("published roots are unavailable")
		}
		generation := snapshot.roots[domain]
		if generation == nil {
			return nil, errors.New("published site roots are unavailable")
		}
		if !generation.acquire() {
			continue
		}
		if runtime.current.Load() == snapshot {
			return generation, nil
		}
		generation.release()
	}
}

func (catalog *Catalog) RuntimeEnabled() bool {
	return catalog.runtime != nil
}

func (catalog *Catalog) OpenFile(domain, directory, relative string, decisions ...Decision) (*os.File, error) {
	var generation *publicGeneration
	if len(decisions) != 0 {
		generation = decisions[0].generation
		if decisions[0].Domain != domain || generation == nil || !generation.acquire() {
			return nil, errors.New("public publication is unavailable")
		}
	} else {
		var err error
		generation, err = catalog.runtime.acquire(domain)
		if err != nil {
			return nil, err
		}
	}
	defer generation.release()
	root := generation.roots[directory]
	if root == nil {
		return nil, os.ErrNotExist
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, err
	}
	if !catalog.runtime.admits(domain, generation) {
		file.Close()
		return nil, errors.New("public publication changed while opening its file")
	}
	return file, nil
}

func (runtime *PublicRuntime) admits(domain string, generation *publicGeneration) bool {
	snapshot := runtime.current.Load()
	return snapshot != nil && snapshot.roots[domain] == generation && !snapshot.blocked[domain]
}

func (catalog *Catalog) OpenPublished(decision Decision, relative, version string) (*PublishedFile, error) {
	generation := decision.generation
	if generation == nil || !generation.acquire() {
		return nil, errors.New("public publication is unavailable")
	}
	directory := "static"
	if version != "" {
		decoded, err := hex.DecodeString(version)
		if err != nil || len(decoded) != 32 || version != strings.ToLower(version) {
			generation.release()
			return nil, errors.New("invalid published version")
		}
		directory, relative = "versions", version
	}
	root := generation.roots[directory]
	if root == nil {
		generation.release()
		return nil, os.ErrNotExist
	}
	file, err := root.Open(relative)
	if err != nil {
		generation.release()
		if MissingSource(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	if !catalog.runtime.admits(decision.Domain, generation) {
		file.Close()
		generation.release()
		return nil, errors.New("public publication changed while opening its file")
	}
	prepared := generation.html[preparedHTMLKey(directory, relative, decision.pathname)]
	if prepared == nil {
		prepared = generation.html[directory+"\x00"+relative]
	}
	return &PublishedFile{File: file, HTML: prepared, generation: generation}, nil
}

// Preparation runs in the caller's publication session, before transferring
// ownership to the catalog. The catalog never opens roots inside its select.
func (runtime *PublicRuntime) prepare(policies map[string]cachedPolicy, stop <-chan struct{}, skipBlocked bool) (map[string]*publicGeneration, error) {
	if runtime.options.Root == "" || runtime.options.StorageName == nil {
		return nil, errors.New("public root preparation is not configured")
	}
	prepared := make(map[string]*publicGeneration)
	storage, err := os.OpenRoot(runtime.options.Root)
	if err != nil {
		return nil, err
	}
	defer storage.Close()
	domains := make([]string, 0, len(policies))
	for domain := range policies {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	for _, domain := range domains {
		cached := policies[domain]
		select {
		case <-stop:
			retireGenerations(prepared)
			return nil, errors.New("public preparation stopped")
		default:
		}
		if cached.policy.Domain != domain {
			continue
		}
		// Startup already isolated failed sites with durable barriers. An
		// inaccessible blocked site must not prevent healthy sites from opening.
		if skipBlocked && cached.blocked {
			continue
		}
		generation := &publicGeneration{references: make(map[string][]string), pageSources: make(map[string]string), ramMisses: make(map[string]string), roots: make(map[string]*os.Root), html: make(map[string]*PreparedHTML), ram: make(map[string]*preparedRAMFile), assets: make(map[string]*preparedRAMFile), ramBodies: make(map[string]*ramBody), cache: runtime.cache, released: make(chan struct{})}
		if reason := runtime.ramPageReason(cached.policy, "/"); reason != "" {
			generation.ramMisses["/"] = reason
		}
		prepared[domain] = generation
		directories := map[string]string{
			"static":   filepath.Join("storage", "static", runtime.options.StorageName(domain)),
			"files":    filepath.Join("storage", "files", runtime.options.StorageName(domain)),
			"versions": filepath.Dir(sitefreeze.ObjectPath(domain, strings.Repeat("0", 64))),
		}
		for directory, pathname := range directories {
			root, openErr := storage.OpenRoot(pathname)
			if os.IsNotExist(openErr) {
				continue
			}
			if openErr != nil {
				retireGenerations(prepared)
				return nil, openErr
			}
			generation.roots[directory] = root
		}
		if runtime.options.Mode.Enabled(StaticHTML) && !cached.policy.PrivateTestDrive {
			if err = runtime.prepareHTML(generation, cached.policy, stop); err != nil {
				retireGenerations(prepared)
				return nil, err
			}
		}
		if err = runtime.prepareRAMAssets(generation, cached.policy, stop); err != nil {
			retireGenerations(prepared)
			return nil, err
		}
	}
	if err = runtime.admitRAM(prepared, policies, stop); err != nil {
		retireGenerations(prepared)
		return nil, err
	}
	return prepared, nil
}
