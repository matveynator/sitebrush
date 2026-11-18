package publicsite

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
)

const MaximumRAMRepresentationBytes = 256 << 10

// Policy diagnostics expose only flags and counts, never addresses, credentials
// or fingerprints. A nil policy keeps older profiler participants recognizable.
type RAMPolicyStats struct {
	RoutingVersion       int  `json:"routingVersion"`
	Frozen               bool `json:"frozen"`
	Versioned            bool `json:"versioned"`
	PrivateTestDrive     bool `json:"privateTestDrive"`
	PendingRegistration  bool `json:"pendingRegistration"`
	Stealth              bool `json:"stealth"`
	AdministratorIPRules int  `json:"administratorIPRules"`
	ServerIPRules        int  `json:"serverIPRules"`
	PreparedPages        int  `json:"preparedPages"`
	PreparedFiles        int  `json:"preparedFiles"`
	PublishedPages       int  `json:"publishedPages"`
	RootPrepared         bool `json:"rootPrepared"`
	IndexPrepared        bool `json:"indexPrepared"`
	RootPublished        bool `json:"rootPublished"`
}

type RAMSiteStats struct {
	Policy         *RAMPolicyStats `json:"policy,omitempty"`
	RecoveredPages int             `json:"recoveredPages"`
	RecoveredFiles int             `json:"recoveredFiles"`
	RootRecovered  bool            `json:"rootRecovered"`
	Domain         string          `json:"domain"`
	Routes         int             `json:"routes"`
	RootVariants   []string        `json:"rootVariants,omitempty"`
	RootMiss       string          `json:"rootMiss,omitempty"`
}

type RAMCacheStats struct {
	Sites  []RAMSiteStats `json:"sites,omitempty"`
	Limit  int64          `json:"limit"`
	Bytes  int64          `json:"bytes"`
	Bodies int            `json:"bodies"`
}

type ramBody struct {
	bytes  []byte
	length string
}

type ramEntry struct {
	key        string
	size       int64
	references int
	body       *ramBody
}

type ramJob struct {
	action    string
	key       string
	size      int64
	ticket    uint64
	body      *ramBody
	tickets   []uint64
	reply     chan ramResult
	abandoned <-chan struct{}
}

type ramResult struct {
	ticket uint64
	body   *ramBody
	stats  RAMCacheStats
}

// Only publication workers contact this owner. Visitors read immutable bodies
// through their generation and never participate in cache coordination.
type ramCache struct {
	jobs chan ramJob
	stop <-chan struct{}
}

func newRAMCache(limit int64, stop <-chan struct{}) *ramCache {
	cache := &ramCache{jobs: make(chan ramJob), stop: stop}
	go cache.run(limit)
	return cache
}

func (cache *ramCache) run(limit int64) {
	entries := make(map[string]*ramEntry)
	leases := make(map[uint64]*ramEntry)
	var sequence uint64
	var used int64
	release := func(ticket uint64) {
		entry := leases[ticket]
		if entry == nil {
			return
		}
		delete(leases, ticket)
		entry.references--
		if entry.references == 0 {
			used -= entry.size
			if entries[entry.key] == entry {
				delete(entries, entry.key)
			}
		}
	}
	for {
		select {
		case <-cache.stop:
			return
		case job := <-cache.jobs:
			if job.action == "release" {
				for _, ticket := range job.tickets {
					release(ticket)
				}
				continue
			}
			select {
			case <-job.abandoned:
				continue
			default:
			}
			result := ramResult{}
			switch job.action {
			case "reserve":
				entry := entries[job.key]
				if entry == nil && job.size > 0 && job.size <= limit-used {
					entry = &ramEntry{key: job.key, size: job.size}
					used += job.size
				}
				if entry != nil {
					sequence++
					entry.references++
					leases[sequence] = entry
					result.ticket, result.body = sequence, entry.body
				}
			case "commit":
				entry := leases[job.ticket]
				if entry != nil && job.body != nil && int64(len(job.body.bytes)) == entry.size {
					if existing := entries[entry.key]; existing != nil && existing != entry {
						release(job.ticket)
						existing.references++
						leases[job.ticket] = existing
						entry = existing
					} else {
						entry.body = job.body
						entries[entry.key] = entry
					}
					result.ticket, result.body = job.ticket, entry.body
				}
			case "stats":
				result.stats = RAMCacheStats{Limit: limit, Bytes: used, Bodies: len(entries)}
			}
			select {
			case job.reply <- result:
			case <-job.abandoned:
				release(result.ticket)
			case <-cache.stop:
				return
			}
		}
	}
}

func (cache *ramCache) exchange(job ramJob, stop <-chan struct{}) (ramResult, error) {
	job.reply, job.abandoned = make(chan ramResult), stop
	select {
	case cache.jobs <- job:
	case <-stop:
		return ramResult{}, errors.New("RAM preparation stopped")
	case <-cache.stop:
		return ramResult{}, errors.New("RAM cache stopped")
	}
	select {
	case result := <-job.reply:
		return result, nil
	case <-stop:
		return ramResult{}, errors.New("RAM preparation stopped")
	case <-cache.stop:
		return ramResult{}, errors.New("RAM cache stopped")
	}
}

func (cache *ramCache) release(tickets []uint64) {
	if cache == nil || len(tickets) == 0 {
		return
	}
	select {
	case cache.jobs <- ramJob{action: "release", tickets: tickets}:
	case <-cache.stop:
	}
}

func (catalog *Catalog) RAMStats(stop <-chan struct{}) (RAMCacheStats, error) {
	if catalog == nil || catalog.runtime == nil || catalog.runtime.cache == nil {
		return RAMCacheStats{}, nil
	}
	result, err := catalog.runtime.cache.exchange(ramJob{action: "stats"}, stop)
	if snapshot := catalog.runtime.current.Load(); snapshot != nil {
		for domain, generation := range snapshot.roots {
			policy := snapshot.policies[domain].policy
			indexPath := "/" + filepath.ToSlash(catalog.runtime.options.StaticPath("/"))
			site := RAMSiteStats{Domain: domain, RootMiss: generation.ramMisses["/"], RecoveredPages: generation.recoveredPages, RecoveredFiles: generation.recoveredFiles,
				Policy: &RAMPolicyStats{RoutingVersion: policy.RoutingVersion, Frozen: policy.Frozen, Versioned: policy.Versioned, PrivateTestDrive: policy.PrivateTestDrive, PendingRegistration: policy.PendingRegistration, Stealth: policy.Stealth,
					AdministratorIPRules: len(policy.AllowedIPs), ServerIPRules: len(policy.ServerIPs), PreparedPages: len(policy.PreparedPages), PreparedFiles: len(policy.PreparedFiles), PublishedPages: len(policy.PublishedVersions), RootPrepared: policy.PreparedPages["/"] != "", IndexPrepared: policy.PreparedPages[indexPath] != "", RootPublished: policy.PublishedVersions["/"] != ""}}
			for _, response := range generation.ram {
				if len(response.variants) != 0 {
					site.Routes++
				}
			}
			if response := generation.ram["/"]; response != nil {
				site.RootMiss = response.reason
				site.RootRecovered = response.recovered
				for variant := range response.variants {
					site.RootVariants = append(site.RootVariants, fmt.Sprintf("%s/menu=%t/updating=%t", variant.Language, variant.Menu, variant.Updating))
				}
				sort.Strings(site.RootVariants)
				if len(site.RootVariants) == len(response.prepared.variants) {
					site.RootMiss = ""
				}
			}
			if len(site.RootVariants) == 0 && site.RootMiss == "" {
				site.RootMiss = "no_prepared_object"
			}
			if reason := catalog.runtime.ramPageReason(policy, "/"); reason != "" {
				site.RootMiss = reason
			}
			result.stats.Sites = append(result.stats.Sites, site)
		}
		sort.Slice(result.stats.Sites, func(first, second int) bool {
			return result.stats.Sites[first].Domain < result.stats.Sites[second].Domain
		})
	}
	return result.stats, err
}
