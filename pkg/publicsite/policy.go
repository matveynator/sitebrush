package publicsite

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/matveynator/sitebrush/v2/pkg/dirprotect"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type FileRule struct {
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
type Policy struct {
	Chroots       []Chroot
	Domain        string
	Frozen        bool
	Stealth       bool
	AllowedIPs    []string
	Aliases       []string
	Files         []FileRule
	Passwords     []dirprotect.Rule
	Redirects     []Redirect
	OwnerSessions []Session
}
type Lookup struct {
	Host        string
	Path        string
	File        string
	SessionHash string
}
type Decision struct {
	Chroot     Chroot
	Domain     string
	Frozen     bool
	Stealth    bool
	AllowedIPs []string
	Password   dirprotect.Rule
	File       FileRule
	Redirect   string
	Owner      bool
	Found      bool
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
		return err
	}
	if err = storage.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	file, err := storage.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	return file.Close()
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
	return nil
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
	return root.Rename(temporary, name)
}

type lookupTask struct {
	query Lookup
	reply chan Decision
}
type Catalog struct {
	tasks chan lookupTask
	stop  <-chan struct{}
}
type cachedPolicy struct {
	files     map[string]FileRule
	redirects map[string]string
	policy    Policy
	modified  time.Time
	size      int64
}

func NewCatalog(root string, stop <-chan struct{}) *Catalog {
	catalog := &Catalog{tasks: make(chan lookupTask, 1024), stop: stop}
	go func() {
		policies := make(map[string]cachedPolicy)
		var usedBytes int64
		storage, rootErr := os.OpenRoot(root)
		if rootErr == nil {
			defer storage.Close()
		}
		read := func(host string) (cachedPolicy, bool) {
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
					cached.files[rule.Name] = rule
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
			case task := <-catalog.tasks:
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
					blockName, _ := filepath.Rel(root, BlockPath(root, policy.Domain))
					_, blockErr := storage.Stat(blockName)
					found = os.IsNotExist(blockErr)
				}
				if found {
					decision = Decision{Domain: policy.Domain, Frozen: policy.Frozen, Stealth: policy.Stealth, AllowedIPs: append([]string(nil), policy.AllowedIPs...), Found: true}
					for _, rule := range policy.Passwords {
						if dirprotect.HasProtectedPrefix(task.query.Path, rule.Path) && len(rule.Path) >= len(decision.Password.Path) {
							decision.Password = rule
						}
					}
					decision.File = cached.files[task.query.File]

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
				task.reply <- decision
			}
		}
	}()
	return catalog
}
func (catalog *Catalog) Lookup(query Lookup) Decision {
	reply := make(chan Decision, 1)
	select {
	case catalog.tasks <- lookupTask{query: query, reply: reply}:
	case <-catalog.stop:
		return Decision{}
	}
	select {
	case result := <-reply:
		return result
	case <-catalog.stop:
		return Decision{}
	}
}
