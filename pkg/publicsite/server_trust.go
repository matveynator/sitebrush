package publicsite

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const serverPolicyIndex = "storage/public-sites/.server-policy"

// The installation has one server-owner site. Its contact is independent of
// the bounded content cache, so evicting a site's policy cannot revoke trust.
func writeServerPolicyIndex(storage *os.Root, policy Policy) error {
	previous, err := readServerPolicyDomain(storage)
	if err != nil && !os.IsNotExist(err) && len(policy.ServerIPs) == 0 {
		return nil
	}
	if len(policy.ServerIPs) == 0 && previous != policy.Domain {
		return nil
	}
	domain := policy.Domain
	if len(policy.ServerIPs) == 0 {
		domain = ""
	}
	return atomicPolicy(storage, serverPolicyIndex, Policy{Domain: domain})
}
func readServerPolicyDomain(storage *os.Root) (string, error) {
	file, err := storage.Open(serverPolicyIndex)
	if err != nil {
		return "", err
	}
	defer file.Close()
	var document struct{ Domain string }
	err = json.NewDecoder(io.LimitReader(file, 4096)).Decode(&document)
	return document.Domain, err
}

type serverTrustCache struct {
	domain        string
	rules         []TrustedIP
	modified      time.Time
	size          int64
	indexModified time.Time
	indexSize     int64
	indexed       bool
	discovered    bool
	blocked       bool
}

// Only the catalog participant reads or changes this cache. Disk mode checks
// the authoritative owner's policy and barrier rather than the content cache.
func (trust *serverTrustCache) refresh(storage *os.Root, force bool, stop <-chan struct{}) {
	select {
	case <-stop:
		return
	default:
	}
	index, err := storage.Stat(serverPolicyIndex)
	switch {
	case err == nil:
		if force || !trust.indexed || !index.ModTime().Equal(trust.indexModified) || index.Size() != trust.indexSize {
			domain, readErr := readServerPolicyDomain(storage)
			if readErr != nil {
				trust.rules = nil
				trust.blocked = true
				return
			}
			if domain != trust.domain {
				trust.modified = time.Time{}
				trust.rules = nil
			}
			trust.domain = domain
			trust.indexModified = index.ModTime()
			trust.indexSize = index.Size()
			trust.indexed = true
			force = true
		}
	case os.IsNotExist(err):
		if trust.indexed {
			trust.domain = ""
			trust.rules = nil
			return
		}
		if !trust.discovered {
			trust.domain = discoverServerPolicy(storage, stop)
			trust.discovered = true
		}
	default:
		trust.rules = nil
		trust.blocked = true
		return
	}
	if trust.domain == "" {
		trust.rules = nil
		return
	}
	name := filepath.Join("storage", "public-sites", policyName(trust.domain))
	info, err := storage.Stat(name)
	if err != nil || info.Size() > 8<<20 {
		trust.rules = nil
		trust.blocked = true
		return
	}
	if force || !info.ModTime().Equal(trust.modified) || info.Size() != trust.size {
		trust.rules = nil
		file, openErr := storage.Open(name)
		if openErr != nil {
			trust.blocked = true
			return
		}
		var document struct {
			Domain    string
			ServerIPs []TrustedIP
		}
		readErr := json.NewDecoder(io.LimitReader(file, (8<<20)+1)).Decode(&document)
		file.Close()
		trust.modified = info.ModTime()
		trust.size = info.Size()
		if readErr == nil && document.Domain == trust.domain {
			trust.rules = document.ServerIPs
		}
	}
	_, blockErr := storage.Stat(name + ".blocked")
	trust.blocked = !os.IsNotExist(blockErr)
	current, statErr := storage.Stat(name)
	if statErr != nil || !current.ModTime().Equal(trust.modified) || current.Size() != trust.size {
		trust.blocked = true
	}
}

// Older snapshots predate the small owner contact. Discover it once without
// retaining layouts, file rules, or a map of every site in the installation.
func discoverServerPolicy(storage *os.Root, stop <-chan struct{}) string {
	directory, err := storage.Open(filepath.Join("storage", "public-sites"))
	if err != nil {
		return ""
	}
	defer directory.Close()
	domain := ""
	for {
		select {
		case <-stop:
			return ""
		default:
		}
		entries, readErr := directory.ReadDir(128)
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil || info.Size() > 8<<20 {
				continue
			}
			file, openErr := storage.Open(filepath.Join("storage", "public-sites", entry.Name()))
			if openErr != nil {
				continue
			}
			var document struct {
				Domain    string
				ServerIPs []TrustedIP
			}
			decodeErr := json.NewDecoder(io.LimitReader(file, (8<<20)+1)).Decode(&document)
			file.Close()
			if decodeErr != nil || document.Domain == "" || entry.Name() != policyName(document.Domain) || len(document.ServerIPs) == 0 {
				continue
			}
			if domain != "" && domain != document.Domain {
				return ""
			}
			domain = document.Domain
		}
		if readErr != nil {
			if readErr != io.EOF {
				return ""
			}
			return domain
		}
	}
}
