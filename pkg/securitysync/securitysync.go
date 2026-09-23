package securitysync

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	evidenceWindow       = 24 * time.Hour
	globalBlockTTL       = 7 * 24 * time.Hour
	requiredSources      = 3
	maximumTrackedIPs    = 4096
	maximumSourcesPerIP  = 16
)

type Signal struct {
	IP             string    `json:"ip"`
	Category       string    `json:"category"`
	Description    string    `json:"description"`
	InstallationID string    `json:"installation_id"`
	ObservedAt     time.Time `json:"observed_at"`
}

type Entry struct {
	IP            string    `json:"ip"`
	Reason        string    `json:"reason"`
	LastEvent     time.Time `json:"last_event"`
	ExpiresAt     time.Time `json:"expires_at"`
	Confirmations int       `json:"confirmations"`
}

type Request struct {
	Signal *Signal
	Query  bool
	Reply  chan Result
}

type Result struct {
	Accepted bool
	Entries  []Entry
	Err      error
}

type evidenceSource struct {
	InstallationID string    `json:"installation_id"`
	Category       string    `json:"category"`
	ObservedAt     time.Time `json:"observed_at"`
}

type evidenceRecord struct {
	IP      string           `json:"ip"`
	Sources []evidenceSource `json:"sources"`
}

type diskState struct {
	Records []evidenceRecord `json:"records"`
}

func Start(path string, stop <-chan struct{}) (chan<- Request, error) {
	records, err := load(path)
	if err != nil {
		return nil, err
	}
	requests := make(chan Request, 64)
	go run(path, stop, requests, records)
	return requests, nil
}

// run owns all reputation maps. NetChan handlers submit bounded jobs and never
// coordinate global security state through shared memory.
func run(path string, stop <-chan struct{}, requests <-chan Request, records map[string]map[string]evidenceSource) {
	pruneTicker := time.NewTicker(time.Hour)
	defer pruneTicker.Stop()
	for {
		select {
		case <-stop:
			_ = save(path, records, time.Now().UTC())
			return
		case now := <-pruneTicker.C:
			prune(records, now.UTC())
		case request := <-requests:
			now := time.Now().UTC()
			result := Result{}
			if request.Signal != nil {
				result.Accepted, result.Err = submit(records, *request.Signal, now)
				if result.Accepted && result.Err == nil {
					_ = save(path, records, now)
				}
			}
			if request.Query {
				result.Entries = approved(records, now)
			}
			if request.Reply != nil {
				select {
				case request.Reply <- result:
				case <-stop:
					return
				}
			}
		}
	}
}

func submit(records map[string]map[string]evidenceSource, signal Signal, now time.Time) (bool, error) {
	ip := net.ParseIP(strings.TrimSpace(signal.IP))
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() {
		return false, errors.New("security signal IP is invalid")
	}
	signal.IP = ip.String()
	signal.InstallationID = strings.TrimSpace(signal.InstallationID)
	if signal.InstallationID == "" || len(signal.InstallationID) > 128 {
		return false, errors.New("security signal installation is invalid")
	}
	if !globalCategory(signal.Category) {
		return false, errors.New("security signal category is not eligible")
	}
	if signal.ObservedAt.IsZero() || now.Sub(signal.ObservedAt) > evidenceWindow || signal.ObservedAt.Sub(now) > 2*time.Minute {
		return false, errors.New("security signal time is invalid")
	}
	prune(records, now)
	sources := records[signal.IP]
	if sources == nil {
		if len(records) >= maximumTrackedIPs {
			return false, errors.New("security reputation capacity reached")
		}
		sources = map[string]evidenceSource{}
		records[signal.IP] = sources
	}
	if _, exists := sources[signal.InstallationID]; !exists && len(sources) >= maximumSourcesPerIP {
		return false, errors.New("security signal source capacity reached")
	}
	sources[signal.InstallationID] = evidenceSource{
		InstallationID: signal.InstallationID,
		Category:       signal.Category,
		ObservedAt:     signal.ObservedAt.UTC(),
	}
	return true, nil
}

func approved(records map[string]map[string]evidenceSource, now time.Time) []Entry {
	prune(records, now)
	entries := make([]Entry, 0)
	for ip, sources := range records {
		if len(sources) < requiredSources {
			continue
		}
		lastEvent := time.Time{}
		reason := ""
		for _, source := range sources {
			if source.ObservedAt.After(lastEvent) {
				lastEvent = source.ObservedAt
				reason = source.Category
			}
		}
		entries = append(entries, Entry{
			IP:            ip,
			Reason:        reason,
			LastEvent:     lastEvent,
			ExpiresAt:     lastEvent.Add(globalBlockTTL),
			Confirmations: len(sources),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Confirmations == entries[j].Confirmations {
			return entries[i].LastEvent.After(entries[j].LastEvent)
		}
		return entries[i].Confirmations > entries[j].Confirmations
	})
	return entries
}

func prune(records map[string]map[string]evidenceSource, now time.Time) {
	cutoff := now.Add(-evidenceWindow)
	for ip, sources := range records {
		for installationID, source := range sources {
			if source.ObservedAt.Before(cutoff) {
				delete(sources, installationID)
			}
		}
		if len(sources) == 0 {
			delete(records, ip)
		}
	}
}

func globalCategory(category string) bool {
	switch strings.TrimSpace(category) {
	case "injection", "traversal", "repository", "secret", "source-backup", "scanner-client", "enumeration":
		return true
	default:
		return false
	}
}

func load(path string) (map[string]map[string]evidenceSource, error) {
	records := map[string]map[string]evidenceSource{}
	if strings.TrimSpace(path) == "" {
		return records, nil
	}
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	state := diskState{}
	if err := json.Unmarshal(encoded, &state); err != nil {
		return nil, err
	}
	for _, record := range state.Records {
		ip := net.ParseIP(strings.TrimSpace(record.IP))
		if ip == nil {
			continue
		}
		sources := map[string]evidenceSource{}
		for _, source := range record.Sources {
			if source.InstallationID != "" {
				sources[source.InstallationID] = source
			}
		}
		if len(sources) > 0 {
			records[ip.String()] = sources
		}
	}
	prune(records, time.Now().UTC())
	return records, nil
}

func save(path string, records map[string]map[string]evidenceSource, now time.Time) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	prune(records, now)
	state := diskState{}
	for ip, sources := range records {
		record := evidenceRecord{IP: ip}
		for _, source := range sources {
			record.Sources = append(record.Sources, source)
		}
		sort.Slice(record.Sources, func(i, j int) bool {
			return record.Sources[i].InstallationID < record.Sources[j].InstallationID
		})
		state.Records = append(state.Records, record)
	}
	sort.Slice(state.Records, func(i, j int) bool { return state.Records[i].IP < state.Records[j].IP })
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".reputation-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err = temporary.Chmod(0600); err == nil {
		_, err = temporary.Write(encoded)
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
