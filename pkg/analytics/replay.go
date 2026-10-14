package analytics

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
)

const ReplayRetention = 7 * 24 * time.Hour
const MaximumReplayEvents = 256
const MaximumReplayDocument = 32768

type ReplayPreferences struct {
	Enabled bool `json:"enabled"`
	Sample  int  `json:"sample"`
}
type ReplayEvent struct {
	Kind         string `json:"kind"`
	Element      string `json:"element,omitempty"`
	Scroll       int    `json:"scroll,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	Filled       bool   `json:"filled,omitempty"`
	At           int64  `json:"at"`
	Path, Layout string
	Time         time.Time
}
type ReplayPage struct {
	Path, Layout, HTML string
	Unavailable        bool
}
type ReplaySession struct {
	GoalConfigured, Completed bool
	ID                        string
	Started, Last             time.Time
	Events                    []ReplayEvent
	Reasons                   []string
	Limited                   bool
}
type ReplayState struct {
	Sessions map[string]*ReplaySession
	Pages    map[string]*ReplayPage
	Used     int64
}
type ReplaySummary struct {
	ID            string
	Started, Last time.Time
	Events        int
	Reasons       []string
	Limited       bool
}

func ValidateReplayPreferences(preferences ReplayPreferences) error {
	if preferences.Sample < 1 || preferences.Sample > 10 {
		return errors.New("replay sample must be between 1 and 10 percent")
	}
	return nil
}
func ReplaySampled(session string, preferences ReplayPreferences) bool {
	if !preferences.Enabled || ValidateReplayPreferences(preferences) != nil || session == "" {
		return false
	}
	digest := sha256.Sum256([]byte(session))
	return int(binary.BigEndian.Uint64(digest[:8])%100) < preferences.Sample
}
func ValidReplay(events []ReplayEvent) bool {
	if len(events) > MaximumSignals {
		return false
	}
	previous := int64(0)
	for _, event := range events {
		switch event.Kind {
		case "navigation", "scroll", "click", "touch", "focus", "resize", "field", "error":
		default:
			return false
		}
		if event.Element != "" && !safeStructuralKey(event.Element) || event.Scroll < 0 || event.Scroll > 100 || event.Width < 0 || event.Width > 32768 || event.Height < 0 || event.Height > 32768 || event.At < previous || event.At > 24*60*60*1000 {
			return false
		}
		previous = event.At
	}
	return true
}

// Replay has its own small budget. It cannot evict accepted conversion data,
// and retains only typed actions plus sanitized authored page snapshots.
func (site *Site) recordReplay(event Event, now time.Time, limit int64) {
	if !event.ReplayAllowed || site.Conversion == nil {
		return
	}
	state := site.Conversion
	if state.Replay == nil {
		state.Replay = &ReplayState{Sessions: map[string]*ReplaySession{}, Pages: map[string]*ReplayPage{}}
	}
	replay := state.Replay
	digest := sha256.Sum256([]byte(event.Visitor + ":" + event.Session))
	identifier := hex.EncodeToString(digest[:8])
	session := replay.Sessions[identifier]
	if session == nil {
		if len(event.Replay) == 0 {
			return
		}
		if len(replay.Sessions) >= 24 || replay.Used+2048 > limit/8 || state.Used+2048 > limit {
			return
		}
		session = &ReplaySession{ID: identifier, Started: now}
		replay.Sessions[identifier] = session
		replay.Used += 2048
		state.Used += 2048
	}
	session.Last = now
	for _, goal := range site.Goals {
		if goal.Primary || len(site.Goals) == 1 {
			session.GoalConfigured = true
			if visitor := state.Visitors[event.Visitor]; visitor != nil {
				if progress := visitor.Funnels[GoalKey(goal)]; progress != nil && len(progress.Reached) == len(goalSteps(goal)) {
					session.Completed = true
				}
			}
			break
		}
	}

	for _, signal := range event.Signals {
		switch signal.Kind {
		case "rage-click", "dead-click", "form-failed":
			addReplayReason(session, signal.Kind)
		}
	}
	lastAt := int64(0)
	if len(event.Replay) > 0 {
		lastAt = event.Replay[len(event.Replay)-1].At
	}
	for _, action := range event.Replay {
		if len(session.Events) >= MaximumReplayEvents || replay.Used+256 > limit/8 || state.Used+256 > limit {
			session.Limited = true
			break
		}
		action.Path = event.Path
		action.Layout = event.Layout
		action.Time = now.Add(time.Duration(action.At-lastAt) * time.Millisecond)
		session.Events = append(session.Events, action)
		replay.Used += 256
		state.Used += 256
		pageKey := event.Path + "\x1f" + event.Layout
		if replay.Pages[pageKey] == nil && len(replay.Pages) < 32 {
			replay.Pages[pageKey] = &ReplayPage{Path: event.Path, Layout: event.Layout}
		}
		if action.Kind == "error" {
			addReplayReason(session, "ui-error")
		}
	}
}
func addReplayReason(session *ReplaySession, reason string) {
	for _, known := range session.Reasons {
		if known == reason {
			return
		}
	}
	session.Reasons = append(session.Reasons, reason)
}
func (site *Site) PruneReplay(now time.Time) {
	if site.Conversion == nil || site.Conversion.Replay == nil {
		return
	}
	replay := site.Conversion.Replay
	replay.Used = 0
	usedPages := map[string]bool{}
	for identifier, session := range replay.Sessions {
		if now.Sub(session.Last) > ReplayRetention {
			delete(replay.Sessions, identifier)
			continue
		}
		if session.GoalConfigured && !session.Completed && now.Sub(session.Last) > 30*time.Minute {
			addReplayReason(session, "funnel-drop")
		}
		replay.Used += 2048 + int64(len(session.Events))*256
		for _, event := range session.Events {
			usedPages[event.Path+"\x1f"+event.Layout] = true
		}
	}
	for key, page := range replay.Pages {
		if !usedPages[key] {
			delete(replay.Pages, key)
		} else {
			replay.Used += int64(len(page.HTML)) + 256
		}
	}
	site.Conversion.Used += replay.Used
}
func ReplayDocument(content, page, layout string) (string, error) {
	snapshot, err := BehaviourPreview(strings.NewReader(content), page)
	if err != nil {
		return "", err
	}
	snapshot = strings.Replace(snapshot, "</head>", `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; script-src 'none'; form-action 'none'; base-uri 'none'"></head>`, 1)
	if len(snapshot) > MaximumReplayDocument {
		return "", errors.New("replay document exceeds its budget")
	}
	if !strings.Contains(snapshot, `data-analytics-layout="`+layout+`"`) {
		return "", errors.New("published page version changed before replay capture")
	}
	return snapshot, nil
}
func replaySummaries(state *ReplayState, start, now time.Time) []ReplaySummary {
	summaries := []ReplaySummary{}
	if state == nil {
		return summaries
	}
	for _, session := range state.Sessions {
		if session.Last.Before(start) || session.Started.After(now) {
			continue
		}
		summaries = append(summaries, ReplaySummary{ID: session.ID, Started: session.Started, Last: session.Last, Events: len(session.Events), Reasons: append([]string(nil), session.Reasons...), Limited: session.Limited})
	}
	sort.Slice(summaries, func(left, right int) bool {
		if len(summaries[left].Reasons) != len(summaries[right].Reasons) {
			return len(summaries[left].Reasons) > len(summaries[right].Reasons)
		}
		return summaries[left].Last.After(summaries[right].Last)
	})
	return summaries
}
