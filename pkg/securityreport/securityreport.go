package securityreport

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const retention = 64 * 24 * time.Hour

type Report struct {
	IncidentID string    `json:"incident_id"`
	Domain     string    `json:"domain"`
	IP         string    `json:"ip"`
	Message    string    `json:"message,omitempty"`
	SentAt     time.Time `json:"sent_at"`
}

type Request struct {
	Kind       string
	IncidentID string
	Report     Report
	Success    bool
	Reply      chan Result
}

type Result struct {
	Allowed bool
	Already bool
	Report  Report
	Err     error
}

type state struct {
	Version int      `json:"version"`
	Reports []Report `json:"reports"`
}

// Start owns the incident-report state in one goroutine. Callers communicate
// only through requests and reply channels; no mutable report map is shared.
func Start(path string, stop <-chan struct{}) (chan<- Request, error) {
	reports, err := load(path)
	if err != nil {
		return nil, err
	}
	requests := make(chan Request, 32)
	go run(path, stop, requests, reports)
	return requests, nil
}

func run(path string, stop <-chan struct{}, requests <-chan Request, reports map[string]Report) {
	pending := map[string]bool{}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			_ = save(path, reports, time.Now().UTC())
			return
		case now := <-ticker.C:
			prune(reports, now.UTC())
		case request := <-requests:
			now := time.Now().UTC()
			result := Result{}
			switch request.Kind {
			case "begin":
				id := cleanIncidentID(request.IncidentID)
				if id == "" {
					result.Err = errors.New("invalid incident id")
					break
				}
				prune(reports, now)
				if report, found := reports[id]; found {
					result.Already = true
					result.Report = report
					break
				}
				if pending[id] {
					result.Already = true
					break
				}
				pending[id] = true
				result.Allowed = true
			case "finish":
				id := cleanIncidentID(request.Report.IncidentID)
				if id == "" || !pending[id] {
					result.Err = errors.New("incident report was not claimed")
					break
				}
				delete(pending, id)
				if request.Success {
					report := request.Report
					report.IncidentID = id
					report.Domain = cleanText(report.Domain, 255)
					report.IP = cleanText(report.IP, 64)
					report.Message = cleanText(report.Message, 500)
					if report.SentAt.IsZero() {
						report.SentAt = now
					}
					reports[id] = report
					result.Report = report
					result.Allowed = true
					_ = save(path, reports, now)
				}
			case "get":
				id := cleanIncidentID(request.IncidentID)
				report, found := reports[id]
				result.Already = found
				result.Report = report
			default:
				result.Err = errors.New("unknown incident report operation")
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

func cleanIncidentID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 8 || len(value) > 64 {
		return ""
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return ""
		}
	}
	return value
}

func cleanText(value string, limit int) string {
	value = strings.TrimSpace(strings.Map(func(character rune) rune {
		if character < ' ' && character != '\n' && character != '\t' {
			return -1
		}
		return character
	}, value))
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}

func load(path string) (map[string]Report, error) {
	reports := map[string]Report{}
	if strings.TrimSpace(path) == "" {
		return reports, nil
	}
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return reports, nil
	}
	if err != nil {
		return nil, err
	}
	var stored state
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return nil, err
	}
	for _, report := range stored.Reports {
		if id := cleanIncidentID(report.IncidentID); id != "" {
			report.IncidentID = id
			reports[id] = report
		}
	}
	prune(reports, time.Now().UTC())
	return reports, nil
}

func prune(reports map[string]Report, now time.Time) {
	cutoff := now.Add(-retention)
	for id, report := range reports {
		if !report.SentAt.IsZero() && report.SentAt.Before(cutoff) {
			delete(reports, id)
		}
	}
}

func save(path string, reports map[string]Report, now time.Time) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	prune(reports, now)
	stored := state{Version: 1, Reports: make([]Report, 0, len(reports))}
	for _, report := range reports {
		stored.Reports = append(stored.Reports, report)
	}
	encoded, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".incident-report-*")
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
