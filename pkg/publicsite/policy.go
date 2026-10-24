package publicsite

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/dirprotect"
)

type FileRule struct {
	Version       string
	AccessMode    string
	Name          string
	Mode          string
	Expires       string
	ProtectedPath string
}
type Chroot struct {
	Path      string
	Directory string
}

type Redirect struct {
	From string
	To   string
}
type Session struct {
	Hash    string
	Expires int64
}
type TrustedIP struct {
	Address string
	Expires int64
}

type Policy struct {
	Versioned         bool
	PublishedVersions map[string]string
	ChrootVersions    map[string]string
	FileVersions      map[string]string
	FileAliases       map[string]string
	ChrootAliases     map[string]string
	ChrootListings    map[string][]string
	Hidden            map[string]bool
	Layouts           map[string]string
	TrustedIPs        []TrustedIP
	ServerIPs         []TrustedIP
	Chroots           []Chroot
	Domain            string
	Frozen            bool
	Stealth           bool
	AllowedIPs        []string
	Aliases           []string
	Files             []FileRule
	Passwords         []dirprotect.Rule
	Redirects         []Redirect
	OwnerSessions     []Session
}
type Lookup struct {
	ClientIP    string
	Host        string
	Path        string
	File        string
	SessionHash string
}
type Decision struct {
	PageHidden      bool
	ChrootEntries   []string
	ChrootDirectory bool
	ChrootName      string
	Versioned       bool
	PageVersion     string
	ChrootVersion   string
	Hidden          bool
	Layout          string
	AdminAllowed    bool
	LoadTrusted     bool
	Known           bool
	Chroot          Chroot
	Domain          string
	Frozen          bool
	Stealth         bool
	AllowedIPs      []string
	Password        dirprotect.Rule
	File            FileRule
	Redirect        string
	Owner           bool
	Found           bool
}

func policyName(domain string) string {
	digest := sha256.Sum256([]byte(domain))
	return hex.EncodeToString(digest[:]) + ".json"
}
func PolicyPath(root, domain string) string {
	return filepath.Join(root, "storage", "public-sites", policyName(domain))
}
func BlockPath(root, domain string) string { return PolicyPath(root, domain) + ".blocked" }

// Metadata is private control state, never a site-owned filesystem path.
func SetBlocked(root, domain string, blocked bool) error {
	storage, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer storage.Close()
	name, _ := filepath.Rel(root, BlockPath(root, domain))
	if !blocked {
		err = storage.Remove(name)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return syncPolicyDirectory(storage, name)
	}
	if err = storage.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	file, err := storage.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	return syncPolicyDirectory(storage, name)
}
func Write(root string, policy Policy) error {
	storage, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer storage.Close()
	name, _ := filepath.Rel(root, PolicyPath(root, policy.Domain))
	if err = storage.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	var previous Policy
	if encoded, readErr := storage.ReadFile(name); readErr == nil {
		if decodeErr := json.Unmarshal(encoded, &previous); decodeErr != nil {
			previous = Policy{}
		}
	}
	for _, alias := range previous.Aliases {
		retained := false
		for _, current := range policy.Aliases {
			if alias == current {
				retained = true
				break
			}
		}
		if retained {
			continue
		}
		aliasPath, _ := filepath.Rel(root, PolicyPath(root, alias))
		var pointer Policy
		if encoded, readErr := storage.ReadFile(aliasPath); readErr == nil && json.Unmarshal(encoded, &pointer) == nil && pointer.Domain == policy.Domain {
			if err = storage.Remove(aliasPath); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	if err = atomicPolicy(storage, name, policy); err != nil {
		return err
	}
	for _, alias := range policy.Aliases {
		aliasPath, _ := filepath.Rel(root, PolicyPath(root, alias))
		if err = atomicPolicy(storage, aliasPath, Policy{Domain: policy.Domain}); err != nil {
			return err
		}
	}
	return writeServerPolicyIndex(storage, policy)
}
func atomicPolicy(root *os.Root, name string, policy Policy) error {
	encoded, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(name), ".policy-"+rand.Text())
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	if _, err = file.Write(encoded); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = root.Rename(temporary, name); err != nil {
		return err
	}
	return syncPolicyDirectory(root, name)
}

type lookupTask struct {
	done  <-chan struct{}
	query Lookup
	reply chan Decision
}
type catalogUpdate struct {
	policies map[string]cachedPolicy
	domain   string
	blocked  bool
	memory   *bool
	reply    chan error
}
type Catalog struct {
	root    string
	updates chan catalogUpdate
	tasks   chan lookupTask
	stop    <-chan struct{}
}
type cachedPolicy struct {
	blocked   bool
	files     map[string]FileRule
	redirects map[string]string
	policy    Policy
	modified  time.Time
	size      int64
}

func NewCatalog(root string, stop <-chan struct{}) *Catalog {
	catalog := &Catalog{root: root, updates: make(chan catalogUpdate, 16), tasks: make(chan lookupTask, 1024), stop: stop}
	go func() {
		policies := make(map[string]cachedPolicy)
		var usedBytes int64
		memory := false
		serverTrust := serverTrustCache{}
		blocked := make(map[string]bool)
		storage, rootErr := os.OpenRoot(root)
		if rootErr == nil {
			defer storage.Close()
		}
		if rootErr == nil {
			serverTrust.refresh(storage, true, stop)
		}
		read := func(host string) (cachedPolicy, bool) {
			if memory {
				cached, found := policies[host]
				return cached, found
			}
			if rootErr != nil {
				return cachedPolicy{}, false
			}
			name, relativeErr := filepath.Rel(root, PolicyPath(root, host))
			if relativeErr != nil {
				return cachedPolicy{}, false
			}
			info, err := storage.Stat(name)
			if err != nil {
				return cachedPolicy{}, false
			}
			cached, found := policies[host]
			if !found || !cached.modified.Equal(info.ModTime()) || cached.size != info.Size() {
				if info.Size() > 8<<20 {
					return cachedPolicy{}, false
				}
				file, openErr := storage.Open(name)
				if openErr != nil {
					return cachedPolicy{}, false
				}
				encoded, readErr := io.ReadAll(io.LimitReader(file, (8<<20)+1))
				file.Close()
				if readErr != nil || len(encoded) > 8<<20 {
					return cachedPolicy{}, false
				}
				var policy Policy
				if json.Unmarshal(encoded, &policy) != nil || policy.Domain == "" {
					return cachedPolicy{}, false
				}
				cached = cachedPolicy{policy: policy, modified: info.ModTime(), size: info.Size(), files: make(map[string]FileRule, len(policy.Files)), redirects: make(map[string]string, len(policy.Redirects))}
				for _, rule := range policy.Files {
					cached.files[rule.Name] = admittedFileRule(rule)
				}
				for _, redirect := range policy.Redirects {
					cached.redirects[redirect.From] = redirect.To
				}
				usedBytes -= policies[host].size
				if len(policies) >= 4096 || usedBytes+cached.size > 32<<20 {
					policies = make(map[string]cachedPolicy)
					usedBytes = 0
				}
				policies[host] = cached
				usedBytes += cached.size
			}
			return cached, true
		}
		for {
			select {
			case <-stop:
				return
			case update := <-catalog.updates:
				if update.memory != nil {
					memory = *update.memory
					policies = update.policies
					if policies == nil {
						policies = make(map[string]cachedPolicy)
					}
					blocked = make(map[string]bool)
					usedBytes = 0
					for host, policy := range policies {
						blocked[host] = policy.blocked
						usedBytes += policy.size
					}
				} else if update.policies != nil {
					for host, previous := range policies {
						if previous.policy.Domain == update.domain {
							usedBytes -= previous.size
							delete(policies, host)
						}
					}
					for host, policy := range update.policies {
						policies[host] = policy
						usedBytes += policy.size
					}
					blocked[update.domain] = update.blocked
				} else {
					blocked[update.domain] = update.blocked
				}
				if usedBytes > 32<<20 || len(policies) > 4096 {
					memory = false
					policies = make(map[string]cachedPolicy)
					usedBytes = 0
				}
				if rootErr == nil {
					serverTrust.refresh(storage, true, stop)
				}
				update.reply <- nil
			case task := <-catalog.tasks:
				select {
				case <-task.done:
					continue
				default:
				}
				decision := Decision{}
				cached, found := read(task.query.Host)
				policy := cached.policy
				if found && policy.Domain != task.query.Host {
					cached, found = read(policy.Domain)
					policy = cached.policy
					admittedAlias := false
					for _, alias := range policy.Aliases {
						if alias == task.query.Host {
							admittedAlias = true
							break
						}
					}
					found = found && admittedAlias
				}

				if found {
					decision.Known = true
					decision.Domain = policy.Domain
					if memory {
						found = !blocked[policy.Domain]
					} else {
						blockName, _ := filepath.Rel(root, BlockPath(root, policy.Domain))
						_, blockErr := storage.Stat(blockName)
						found = os.IsNotExist(blockErr)
						if found {
							primaryName, _ := filepath.Rel(root, PolicyPath(root, policy.Domain))
							current, statErr := storage.Stat(primaryName)
							found = statErr == nil && current.ModTime().Equal(cached.modified) && current.Size() == cached.size
						}
					}
				}
				if found {
					decision = Decision{Layout: policy.Layouts[task.query.Path], Known: true, Domain: policy.Domain, Frozen: policy.Frozen, Stealth: policy.Stealth, Found: true}
					decision.PageHidden = policy.Hidden["page\x00"+task.query.Path]
					decision.PageVersion = policy.PublishedVersions[task.query.Path]
					decision.Versioned = policy.Versioned

					if task.query.ClientIP == "" {
						decision.AllowedIPs = append([]string(nil), policy.AllowedIPs...)
					}
					for _, rule := range policy.AllowedIPs {
						if containsIP(rule, task.query.ClientIP) {
							decision.AdminAllowed = true
							break
						}
					}
					decision.LoadTrusted = decision.AdminAllowed || trustedIP(policy.TrustedIPs, task.query.ClientIP)
					if !decision.LoadTrusted {
						if !memory && rootErr == nil {
							serverTrust.refresh(storage, false, stop)
						}
						decision.LoadTrusted = !serverTrust.blocked && !blocked[serverTrust.domain] && trustedIP(serverTrust.rules, task.query.ClientIP)
					}
					for _, rule := range policy.Passwords {
						if dirprotect.HasProtectedPrefix(task.query.Path, rule.Path) && len(rule.Path) >= len(decision.Password.Path) {
							decision.Password = rule
						}
					}
					decision.File = cached.files[task.query.File]
					if policy.Versioned && policy.Frozen && task.query.File != "" {
						resolved := resolveVersionPath(task.query.File, policy.FileAliases)
						decision.File.Version = policy.FileVersions[resolved]
					}
					if hiddenVersionPath(policy.Hidden, "files", task.query.File) || policy.Versioned && policy.Frozen && task.query.File != "" && decision.File.Version == "" {
						decision.Hidden = true
						decision.File.Mode = "hidden"
					}

					if decision.File.Mode == "page_password" {
						decision.Password = dirprotect.Rule{}
						for _, rule := range policy.Passwords {
							if dirprotect.HasProtectedPrefix(decision.File.ProtectedPath, rule.Path) && len(rule.Path) >= len(decision.Password.Path) {
								decision.Password = rule
							}
						}
					}
					for _, location := range policy.Chroots {
						if (task.query.Path == location.Path || strings.HasPrefix(task.query.Path, strings.TrimSuffix(location.Path, "/")+"/")) && len(location.Path) > len(decision.Chroot.Path) {
							decision.Chroot = location
						}
					}
					if decision.Chroot.Path != "" {
						relative := filepath.ToSlash(filepath.Join(strings.TrimPrefix(decision.Chroot.Directory, "/"), strings.TrimPrefix(strings.TrimPrefix(task.query.Path, decision.Chroot.Path), "/")))
						resolved := resolveVersionPath(relative, policy.ChrootAliases)
						decision.ChrootVersion = policy.ChrootVersions[resolved]
						decision.ChrootName = filepath.Base(task.query.Path)
						if decision.ChrootVersion == "" {
							decision.ChrootVersion = policy.ChrootVersions[filepath.ToSlash(filepath.Join(resolved, "index.html"))]
							if decision.ChrootVersion != "" {
								decision.ChrootName = "index.html"
							}
						}
						if entries, exists := policy.ChrootListings[resolved]; exists && decision.ChrootVersion == "" {
							decision.ChrootDirectory = true
							decision.ChrootEntries = append([]string(nil), entries...)
						}
						if hiddenVersionPath(policy.Hidden, "chroot", relative) || policy.Versioned && policy.Frozen && decision.ChrootVersion == "" && !decision.ChrootDirectory {
							decision.Hidden = true
						}
					}
					current := task.query.Path
					for attempt := 0; attempt < 16; attempt++ {
						next := cached.redirects[current]
						if next == "" || next == current || next == task.query.Path {
							break
						}
						current = next
						decision.Redirect = current
					}
					for _, session := range policy.OwnerSessions {
						if session.Hash == task.query.SessionHash && session.Expires > time.Now().Unix() {
							decision.Owner = true
							break
						}
					}
				}
				select {
				case task.reply <- decision:
				case <-task.done:
				case <-stop:
					return
				}
			}
		}
	}()
	return catalog
}
func (catalog *Catalog) Lookup(query Lookup) Decision {
	return catalog.LookupRequest(query, nil)
}
func (catalog *Catalog) LookupRequest(query Lookup, done <-chan struct{}) Decision {
	reply := make(chan Decision, 1)
	select {
	case catalog.tasks <- lookupTask{query: query, reply: reply, done: done}:
	case <-done:
		return Decision{}
	case <-catalog.stop:
		return Decision{}
	}
	select {
	case result := <-reply:
		return result
	case <-done:
		return Decision{}
	case <-catalog.stop:
		return Decision{}
	}
}

// The barrier must survive before a SQL change can make older public metadata
// unsafe. Losing an unremoved barrier is harmless: startup rebuilds the policy.
func syncPolicyDirectory(root *os.Root, name string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := root.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// Disk compatibility remains available until both processes use acknowledged
// policy updates. Only the catalog participant owns its in-memory snapshot.
func (catalog *Catalog) SetMemory(enabled bool) error {
	var policies map[string]cachedPolicy
	if enabled {
		var err error
		policies, err = loadPolicies(catalog.root, "")
		if err != nil {
			return err
		}
	}
	return catalog.update(catalogUpdate{policies: policies, memory: &enabled})
}
func (catalog *Catalog) Reload(domain string) error {
	policies, err := loadPolicies(catalog.root, domain)
	if err != nil {
		return err
	}
	return catalog.update(catalogUpdate{policies: policies, domain: domain, blocked: policies[domain].blocked})
}
func (catalog *Catalog) Open(domain string) error {
	return catalog.update(catalogUpdate{domain: domain})
}
func (catalog *Catalog) Block(domain string) error {
	return catalog.update(catalogUpdate{domain: domain, blocked: true})
}
func (catalog *Catalog) update(update catalogUpdate) error {
	update.reply = make(chan error, 1)
	select {
	case catalog.updates <- update:
	case <-catalog.stop:
		return errors.New("public catalog stopped")
	}
	select {
	case err := <-update.reply:
		return err
	case <-catalog.stop:
		return errors.New("public catalog stopped")
	}
}
func loadPolicies(root, domain string) (map[string]cachedPolicy, error) {
	storage, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer storage.Close()
	names := []string{}
	if domain != "" {
		names = append(names, policyName(domain))
	} else {
		directory, openErr := storage.Open(filepath.Join("storage", "public-sites"))
		if os.IsNotExist(openErr) {
			return make(map[string]cachedPolicy), nil
		}
		if openErr != nil {
			return nil, openErr
		}
		for {
			entries, readErr := directory.ReadDir(128)
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
					names = append(names, entry.Name())
				}
			}
			if len(names) > 4096 {
				directory.Close()
				return nil, errors.New("public policy cache is full")
			}
			if readErr != nil {
				directory.Close()
				if readErr != io.EOF {
					return nil, readErr
				}
				break
			}
		}
	}
	policies := make(map[string]cachedPolicy)
	var used int64
	for _, name := range names {
		filename := filepath.Join("storage", "public-sites", name)
		file, openErr := storage.Open(filename)
		if openErr != nil {
			return nil, openErr
		}
		encoded, readErr := io.ReadAll(io.LimitReader(file, (8<<20)+1))
		file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if len(encoded) > 8<<20 {
			return nil, errors.New("public policy is too large")
		}
		var policy Policy
		if json.Unmarshal(encoded, &policy) != nil || policy.Domain == "" {
			return nil, errors.New("invalid public policy")
		}
		if name != policyName(policy.Domain) {
			continue
		}
		used += int64(len(encoded))
		if used > 32<<20 || len(policies)+len(policy.Aliases) >= 4096 {
			return nil, errors.New("public policy cache is full")
		}
		cached := cachedPolicy{policy: policy, size: int64(len(encoded)), files: make(map[string]FileRule), redirects: make(map[string]string)}
		for _, rule := range policy.Files {
			cached.files[rule.Name] = admittedFileRule(rule)
		}
		for _, redirect := range policy.Redirects {
			cached.redirects[redirect.From] = redirect.To
		}
		blockName, _ := filepath.Rel(root, BlockPath(root, policy.Domain))
		_, blockErr := storage.Stat(blockName)
		cached.blocked = !os.IsNotExist(blockErr)
		policies[policy.Domain] = cached
		for _, alias := range policy.Aliases {
			policies[alias] = cachedPolicy{policy: Policy{Domain: policy.Domain}}
		}
	}
	return policies, nil
}
func containsIP(rule, address string) bool {
	ip, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	if prefix, prefixErr := netip.ParsePrefix(rule); prefixErr == nil {
		return prefix.Contains(ip.Unmap())
	}
	allowed, allowedErr := netip.ParseAddr(rule)
	return allowedErr == nil && allowed.Unmap() == ip.Unmap()
}
func admittedFileRule(rule FileRule) FileRule {
	if rule.Mode == "versioned" {
		rule.Mode = rule.AccessMode
	}
	return rule
}
func trustedIP(rules []TrustedIP, address string) bool {
	now := time.Now().Unix()
	for _, rule := range rules {
		if (rule.Expires == 0 || rule.Expires > now) && containsIP(rule.Address, address) {
			return true
		}
	}
	return false
}

// Resolve only checkpointed relative links, never the live filesystem. A cycle
// or an escaping target has no published version and therefore fails closed.
func resolveVersionPath(name string, aliases map[string]string) string {
	for attempt := 0; attempt < 16; attempt++ {
		longest, target := "", ""
		for source, destination := range aliases {
			if (name == source || strings.HasPrefix(name, source+"/")) && len(source) > len(longest) {
				longest, target = source, destination
			}
		}
		if longest == "" {
			return name
		}
		name = filepath.ToSlash(filepath.Clean(filepath.Join(target, strings.TrimPrefix(strings.TrimPrefix(name, longest), "/"))))
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			return ""
		}
	}
	return ""
}

func hiddenVersionPath(hidden map[string]bool, tree, name string) bool {
	for depth := 0; name != "" && name != "." && depth < 256; depth++ {
		if hidden[tree+"\x00"+name] {
			return true
		}
		parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(name)))
		if parent == name {
			break
		}
		name = parent
	}
	return false
}
