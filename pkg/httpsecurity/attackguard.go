package httpsecurity

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultSecurityBlockTTL     = 24 * time.Hour
	securityReasonHistoryTTL    = 7 * 24 * time.Hour
	securityEscalationHistoryTTL = 64 * 24 * time.Hour
	trustedAdminIPCacheTTL   = 5 * time.Minute
	securityReasonLogLimit   = 24
	attackGuardShardCount    = 16
	attackGuardQueueSize     = 256
	attackGuardWindowLimit   = 1024
)

type SecuritySettings struct {
	AutoBlock  bool `json:"auto_block"`
	GlobalSync bool `json:"global_sync"`
}

type SecurityReasonEvent struct {
	First       time.Time `json:"first,omitempty"`
	At          time.Time `json:"at"`
	Reason      string    `json:"reason"`
	Description string    `json:"description"`
	Source      string    `json:"source"`
	Count       int       `json:"count,omitempty"`
}

type SecurityBlock struct {
	IP            string                `json:"ip"`
	Domain        string                `json:"domain,omitempty"`
	Reason        string                `json:"reason"`
	Description   string                `json:"description"`
	LastEvent     time.Time             `json:"last_event"`
	Source        string                `json:"source"`
	ExpiresAt     time.Time             `json:"expires_at"`
	Violations    int                   `json:"violations"`
	IncidentID    string                `json:"incident_id"`
	ReportedAt    time.Time             `json:"reported_at,omitempty"`
	ReportMessage string                `json:"report_message,omitempty"`
	ReasonLog     []SecurityReasonEvent `json:"reason_log,omitempty"`
}

type SecurityAllow struct {
	IP      string    `json:"ip"`
	Comment string    `json:"comment"`
	AddedAt time.Time `json:"added_at"`
}

type attackWindow struct {
	Started   time.Time
	PostCount int
	Distinct  map[string]struct{}
}

type incidentWindow struct {
	Started time.Time
	Counts  map[string]int
}

type attackGuardDiskState struct {
	Version   int              `json:"version"`
	Settings  SecuritySettings `json:"settings"`
	Blocks    []SecurityBlock  `json:"blocks"`
	Allowlist []SecurityAllow  `json:"allowlist,omitempty"`
}

type attackGuardOperation uint8

const (
	attackGuardCheck attackGuardOperation = iota
	attackGuardObserveFast
	attackGuardObserveIncident
	attackGuardAdd
	attackGuardUpdate
	attackGuardRemove
	attackGuardSnapshot
	attackGuardPersistenceSnapshot
	attackGuardGetSettings
	attackGuardSetSettings
	attackGuardApplyGlobal
	attackGuardAllowAdd
	attackGuardAllowRemove
	attackGuardAllowCheck
	attackGuardAllowSnapshot
	attackGuardTrustAdminIP
	attackGuardCheckAdminIP
	attackGuardClaimAdminIPLookup
	attackGuardClaimIncidentReport
	attackGuardFinishIncidentReport
)

type attackGuardRequest struct {
	Operation   attackGuardOperation
	IP          string
	Domain      string
	Path        string
	Method      string
	StatusCode  int
	Category    string
	Reason      string
	Description string
	Source      string
	Trusted     bool
	Now         time.Time
	ExpiresAt   time.Time
	Settings    SecuritySettings
	Comment     string
	Success     bool
	Reply       chan attackGuardResult
}

type attackGuardResult struct {
	Block        SecurityBlock
	Blocked      bool
	Blocks       []SecurityBlock
	Allowed      bool
	AdminTrusted bool
	Allowlist    []SecurityAllow
	Settings     SecuritySettings
	Changed      bool
	Err          error
}

type AttackGuard struct {
	shards   []chan attackGuardRequest
	save     chan struct{}
	shutdown chan struct{}
	done     chan struct{}
	path     string
}

// NewAttackGuard starts sharded state owners. Each mutable map belongs to exactly
// one goroutine, so request handling never relies on locks or shared map access.
func NewAttackGuard(path string) (*AttackGuard, error) {
	state, err := loadAttackGuardDiskState(path)
	if err != nil {
		return nil, err
	}
	guard := &AttackGuard{
		shards:   make([]chan attackGuardRequest, attackGuardShardCount),
		save:     make(chan struct{}, 1),
		shutdown: make(chan struct{}),
		done:     make(chan struct{}),
		path:     path,
	}
	initialBlocks := make([][]SecurityBlock, attackGuardShardCount)
	initialAllowlist := make([][]SecurityAllow, attackGuardShardCount)
	now := time.Now().UTC()
	for _, block := range state.Blocks {
		block.IP = normalizeSecurityIP(block.IP)
		if block.IP == "" {
			continue
		}
		block.ReasonLog = pruneSecurityReasonLog(block.ReasonLog, now)
		index := attackGuardShardIndex(block.IP)
		initialBlocks[index] = append(initialBlocks[index], block)
	}
	for _, allowed := range state.Allowlist {
		allowed.IP = normalizeSecurityIP(allowed.IP)
		if allowed.IP == "" {
			continue
		}
		index := attackGuardShardIndex(allowed.IP)
		initialAllowlist[index] = append(initialAllowlist[index], allowed)
	}
	for index := range guard.shards {
		requests := make(chan attackGuardRequest, attackGuardQueueSize)
		guard.shards[index] = requests
		go runAttackGuardShard(requests, guard.shutdown, state.Settings, initialBlocks[index], initialAllowlist[index])
	}
	go guard.runPersistence()
	return guard, nil
}

func loadAttackGuardDiskState(path string) (attackGuardDiskState, error) {
	state := attackGuardDiskState{Version: 2, Settings: SecuritySettings{AutoBlock: true, GlobalSync: true}}
	if strings.TrimSpace(path) == "" {
		return state, nil
	}
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(encoded, &state); err != nil {
		return attackGuardDiskState{}, err
	}
	if state.Version < 2 {
		state.Version = 2
		state.Settings.GlobalSync = true
	}
	keptBlocks := state.Blocks[:0]
	for _, block := range state.Blocks {
		if block.Reason == "authentication-failures" && block.Source != "manual" {
			continue
		}
		keptBlocks = append(keptBlocks, block)
	}
	state.Blocks = keptBlocks
	return state, nil
}

func runAttackGuardShard(requests <-chan attackGuardRequest, shutdown <-chan struct{}, settings SecuritySettings, initial []SecurityBlock, initialAllowlist []SecurityAllow) {
	blocks := make(map[string]SecurityBlock, len(initial))
	allowlist := make(map[string]SecurityAllow, len(initialAllowlist))
	trustedAdminIPs := make(map[string]time.Time)
	adminIPLookups := make(map[string]time.Time)
	windows := map[string]*attackWindow{}
	incidents := map[string]*incidentWindow{}
	reportClaims := map[string]bool{}
	for _, block := range initial {
		blocks[block.IP] = block
	}
	for _, allowed := range initialAllowlist {
		allowlist[allowed.IP] = allowed
	}
	pruneTicker := time.NewTicker(time.Minute)
	defer pruneTicker.Stop()

	for {
		select {
		case <-shutdown:
			return
		case now := <-pruneTicker.C:
			pruneAttackGuardState(blocks, windows, incidents, now.UTC())
			pruneAdminIPTrustState(trustedAdminIPs, adminIPLookups, now.UTC())
		case request := <-requests:
			result := handleAttackGuardRequest(blocks, allowlist, trustedAdminIPs, adminIPLookups, windows, incidents, reportClaims, &settings, request)
			if request.Reply != nil {
				select {
				case request.Reply <- result:
				case <-shutdown:
					return
				}
			}
		}
	}
}

func handleAttackGuardRequest(blocks map[string]SecurityBlock, allowlist map[string]SecurityAllow, trustedAdminIPs, adminIPLookups map[string]time.Time, windows map[string]*attackWindow, incidents map[string]*incidentWindow, reportClaims map[string]bool, settings *SecuritySettings, request attackGuardRequest) attackGuardResult {
	now := request.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ip := normalizeSecurityIP(request.IP)
	if request.Domain != "" && (request.Operation == attackGuardObserveFast || request.Operation == attackGuardObserveIncident) {
		trustedAt, trusted := trustedAdminIPs[adminIPTrustKey(request.Domain, ip)]
		if trusted && now.Sub(trustedAt) <= trustedAdminIPCacheTTL {
			return attackGuardResult{Allowed: true, AdminTrusted: true}
		}
		if trusted {
			delete(trustedAdminIPs, adminIPTrustKey(request.Domain, ip))
		}
	}

	switch request.Operation {
	case attackGuardCheck:
		if _, allowed := allowlist[ip]; allowed {
			return attackGuardResult{Allowed: true}
		}
		block, blocked := activeSecurityBlock(blocks, ip, request.Domain, now)
		return attackGuardResult{Block: block, Blocked: blocked}

	case attackGuardObserveFast:
		if _, allowed := allowlist[ip]; allowed {
			return attackGuardResult{Allowed: true}
		}
		if block, blocked := activeSecurityBlock(blocks, ip, request.Domain, now); blocked {
			return attackGuardResult{Block: block, Blocked: true}
		}
		if request.Trusted {
			return attackGuardResult{}
		}
		if !settings.AutoBlock || ip == "" {
			return attackGuardResult{}
		}
		writeRequest := false
		switch strings.ToUpper(strings.TrimSpace(request.Method)) {
		case "POST", "PUT", "PATCH", "DELETE":
			writeRequest = true
		}
		if request.StatusCode == 0 && !writeRequest {
			return attackGuardResult{}
		}
		if request.StatusCode != 0 && request.StatusCode != http.StatusNotFound {
			return attackGuardResult{}
		}
		windowKey := securityDomainIPKey(request.Domain, ip)
		window := windows[windowKey]
		if window == nil || now.Sub(window.Started) > 10*time.Second {
			if window == nil && len(windows) >= attackGuardWindowLimit {
				evictOldestAttackWindow(windows)
			}
			window = &attackWindow{Started: now, Distinct: map[string]struct{}{}}
			windows[windowKey] = window
		}
		if request.StatusCode == 0 {
			if writeRequest {
				window.PostCount++
			}
		}
		if request.StatusCode == http.StatusNotFound && len(window.Distinct) < 256 {
			window.Distinct[boundedPath(request.Path)] = struct{}{}
		}
		if window.PostCount >= 40 || len(window.Distinct) >= 48 {
			reason := "mass-write"
			description := "sent at least 40 write requests within 10 seconds"
			if len(window.Distinct) >= 48 {
				reason = "mass-enumeration"
				description = "scanned at least 48 distinct paths within 10 seconds"
			}
			block := blockLocalSecurityIP(blocks, ip, request.Domain, reason, description, now)
			block.Domain = cleanSecurityText(request.Domain, 255)
			blocks[ip] = block
			delete(windows, windowKey)
			return attackGuardResult{Block: block, Blocked: true, Changed: true}
		}
		return attackGuardResult{}

	case attackGuardObserveIncident:
		if _, allowed := allowlist[ip]; allowed {
			return attackGuardResult{Allowed: true}
		}
		if !settings.AutoBlock || ip == "" || !securityCategoryBlocks(request.Category) {
			return attackGuardResult{}
		}
		threshold := securityCategoryThreshold(request.Category)
		if threshold > 1 {
			windowKey := securityDomainIPKey(request.Domain, ip)
			window := incidents[windowKey]
			if window == nil || now.Sub(window.Started) > time.Minute {
				if window == nil && len(incidents) >= attackGuardWindowLimit {
					evictOldestIncidentWindow(incidents)
				}
				window = &incidentWindow{Started: now, Counts: map[string]int{}}
				incidents[windowKey] = window
			}
			window.Counts[request.Category]++
			if window.Counts[request.Category] < threshold {
				return attackGuardResult{}
			}
			delete(window.Counts, request.Category)
		}
		block := blockLocalSecurityIP(blocks, ip, request.Domain, request.Category, request.Description, now)
		block.Domain = cleanSecurityText(request.Domain, 255)
		blocks[ip] = block
		return attackGuardResult{Block: block, Blocked: true, Changed: true}

	case attackGuardAdd:
		if ip == "" {
			return attackGuardResult{Err: errors.New("invalid IP address")}
		}
		if _, allowed := allowlist[ip]; allowed {
			return attackGuardResult{Err: errors.New("IP is allowlisted")}
		}
		source := cleanSecurityText(request.Source, 32)
		if source == "" {
			source = "manual"
		}
		expiresAt := request.ExpiresAt
		if expiresAt.IsZero() || !expiresAt.After(now) {
			expiresAt = now.Add(defaultSecurityBlockTTL)
		}
		block := blockSecurityIP(blocks, ip, request.Reason, request.Description, source, now, expiresAt.Sub(now))
		return attackGuardResult{Block: block, Blocked: true, Changed: true}

	case attackGuardUpdate:
		block, found := blocks[ip]
		if !found {
			return attackGuardResult{Err: errors.New("block not found")}
		}
		block.Reason = cleanSecurityText(request.Reason, 96)
		block.Description = cleanSecurityText(request.Description, 240)
		block.ReasonLog = appendSecurityReasonEvent(block.ReasonLog, SecurityReasonEvent{First: now, At: now, Reason: block.Reason, Description: block.Description, Source: "manual-edit", Count: 1}, now)
		blocks[ip] = block
		return attackGuardResult{Block: block, Blocked: true, Changed: true}

	case attackGuardRemove:
		_, found := blocks[ip]
		delete(blocks, ip)
		delete(windows, ip)
		delete(incidents, ip)
		return attackGuardResult{Changed: found}

	case attackGuardSnapshot:
		snapshot := make([]SecurityBlock, 0, len(blocks))
		for _, block := range blocks {
			if block.ExpiresAt.IsZero() || now.Before(block.ExpiresAt) {
				snapshot = append(snapshot, block)
			}
		}
		return attackGuardResult{Blocks: snapshot}

	case attackGuardPersistenceSnapshot:
		snapshot := make([]SecurityBlock, 0, len(blocks))
		for _, block := range blocks {
			if block.LastEvent.IsZero() || now.Sub(block.LastEvent) <= securityEscalationHistoryTTL {
				snapshot = append(snapshot, block)
			}
		}
		return attackGuardResult{Blocks: snapshot}

	case attackGuardGetSettings:
		return attackGuardResult{Settings: *settings}

	case attackGuardSetSettings:
		*settings = request.Settings
		return attackGuardResult{Settings: *settings, Changed: true}

	case attackGuardApplyGlobal:
		if ip == "" {
			return attackGuardResult{Err: errors.New("invalid IP address")}
		}
		if _, allowed := allowlist[ip]; allowed {
			return attackGuardResult{Allowed: true}
		}
		expiresAt := request.ExpiresAt
		if expiresAt.IsZero() || !expiresAt.After(now) {
			return attackGuardResult{}
		}
		existing, found := blocks[ip]
		existingActive := found && (existing.ExpiresAt.IsZero() || now.Before(existing.ExpiresAt))
		if existingActive && existing.Source != "global" {
			if existing.ExpiresAt.Before(expiresAt) {
				existing.ExpiresAt = expiresAt
				blocks[ip] = existing
				return attackGuardResult{Block: existing, Blocked: true, Changed: true}
			}
			return attackGuardResult{Block: existing, Blocked: true}
		}
		incidentID := existing.IncidentID
		if incidentID == "" {
			incidentID = randomIncidentID()
		}
		violations := existing.Violations
		if violations < 1 {
			violations = 1
		}
		cleanReason := cleanSecurityText(request.Reason, 96)
		cleanDescription := cleanSecurityText(request.Description, 240)
		reasonLog := append([]SecurityReasonEvent(nil), existing.ReasonLog...)
		reasonLog = appendSecurityReasonEvent(reasonLog, SecurityReasonEvent{First: request.Now, At: request.Now, Reason: cleanReason, Description: cleanDescription, Source: "global", Count: 1}, now)
		block := SecurityBlock{
			IP:          ip,
			Reason:      cleanReason,
			Description: cleanDescription,
			LastEvent:   request.Now,
			Source:      "global",
			ExpiresAt:   expiresAt,
			Violations:  violations,
			IncidentID:  incidentID,
			ReasonLog:   reasonLog,
		}
		blocks[ip] = block
		return attackGuardResult{Block: block, Blocked: true, Changed: true}

	case attackGuardAllowAdd:
		if ip == "" {
			return attackGuardResult{Err: errors.New("invalid IP address")}
		}
		allowed := SecurityAllow{IP: ip, Comment: cleanSecurityText(request.Comment, 240), AddedAt: now}
		allowlist[ip] = allowed
		delete(blocks, ip)
		delete(windows, ip)
		delete(incidents, ip)
		return attackGuardResult{Allowed: true, Changed: true}

	case attackGuardAllowRemove:
		_, found := allowlist[ip]
		delete(allowlist, ip)
		return attackGuardResult{Changed: found}

	case attackGuardAllowCheck:
		_, found := allowlist[ip]
		return attackGuardResult{Allowed: found}

	case attackGuardAllowSnapshot:
		snapshot := make([]SecurityAllow, 0, len(allowlist))
		for _, allowed := range allowlist {
			snapshot = append(snapshot, allowed)
		}
		return attackGuardResult{Allowlist: snapshot}

	case attackGuardTrustAdminIP:
		if request.Domain == "" || ip == "" {
			return attackGuardResult{Err: errors.New("domain and valid IP are required")}
		}
		trustedAdminIPs[adminIPTrustKey(request.Domain, ip)] = now
		delete(adminIPLookups, adminIPTrustKey(request.Domain, ip))
		return attackGuardResult{Allowed: true, Changed: true}

	case attackGuardCheckAdminIP:
		key := adminIPTrustKey(request.Domain, ip)
		trustedAt, trusted := trustedAdminIPs[key]
		if trusted && now.Sub(trustedAt) > trustedAdminIPCacheTTL {
			delete(trustedAdminIPs, key)
			trusted = false
		}
		return attackGuardResult{Allowed: trusted}

	case attackGuardClaimAdminIPLookup:
		key := adminIPTrustKey(request.Domain, ip)
		previousLookup := adminIPLookups[key]
		if !previousLookup.IsZero() && now.Sub(previousLookup) < time.Minute {
			return attackGuardResult{}
		}
		adminIPLookups[key] = now
		return attackGuardResult{Allowed: true}

	case attackGuardClaimIncidentReport:
		block, blocked := activeSecurityBlock(blocks, ip, request.Domain, now)
		if !blocked || block.IncidentID == "" || block.IncidentID != cleanSecurityText(request.Reason, 64) {
			return attackGuardResult{Err: errors.New("security incident not found")}
		}
		if !block.ReportedAt.IsZero() || reportClaims[block.IncidentID] {
			return attackGuardResult{Block: block, Blocked: true}
		}
		reportClaims[block.IncidentID] = true
		return attackGuardResult{Block: block, Blocked: true, Allowed: true}

	case attackGuardFinishIncidentReport:
		block, found := blocks[ip]
		if found && !securityBlockAppliesToDomain(block, request.Domain) {
			found = false
		}
		incidentID := cleanSecurityText(request.Reason, 64)
		if !found || incidentID == "" || block.IncidentID != incidentID || !reportClaims[incidentID] {
			return attackGuardResult{Err: errors.New("security incident report was not claimed")}
		}
		delete(reportClaims, incidentID)
		if !request.Success {
			return attackGuardResult{Block: block, Blocked: true}
		}
		block.ReportedAt = now
		block.ReportMessage = cleanSecurityText(request.Description, 500)
		blocks[ip] = block
		return attackGuardResult{Block: block, Blocked: true, Changed: true}
	}

	return attackGuardResult{Err: errors.New("unknown attack guard operation")}
}

func adminIPTrustKey(domain, ip string) string {
	return strings.ToLower(strings.TrimSpace(domain)) + "\n" + ip
}

func pruneAdminIPTrustState(trustedAdminIPs, adminIPLookups map[string]time.Time, now time.Time) {
	for key, trustedAt := range trustedAdminIPs {
		if now.Sub(trustedAt) > trustedAdminIPCacheTTL {
			delete(trustedAdminIPs, key)
		}
	}
	for key, checkedAt := range adminIPLookups {
		if now.Sub(checkedAt) > time.Hour {
			delete(adminIPLookups, key)
		}
	}
}

func activeSecurityBlock(blocks map[string]SecurityBlock, ip, domain string, now time.Time) (SecurityBlock, bool) {
	if ip == "" {
		return SecurityBlock{}, false
	}
	block, found := blocks[ip]
	if !found {
		return SecurityBlock{}, false
	}
	if !securityBlockAppliesToDomain(block, domain) {
		return SecurityBlock{}, false
	}
	if !block.ExpiresAt.IsZero() && !now.Before(block.ExpiresAt) {
		return SecurityBlock{}, false
	}
	return block, true
}

func securityBlockAppliesToDomain(block SecurityBlock, domain string) bool {
	if block.Source != "local" || block.Domain == "" || domain == "" {
		return true
	}
	return normalizeSecurityDomain(block.Domain) == normalizeSecurityDomain(domain)
}

func securityDomainIPKey(domain, ip string) string {
	return normalizeSecurityDomain(domain) + "\n" + ip
}

func blockLocalSecurityIP(blocks map[string]SecurityBlock, ip, domain, reason, description string, now time.Time) SecurityBlock {
	previous := blocks[ip]
	if previous.Source == "local" && previous.Domain != "" && normalizeSecurityDomain(previous.Domain) != normalizeSecurityDomain(domain) {
		delete(blocks, ip)
	}
	previous = blocks[ip]
	block := blockSecurityIP(blocks, ip, reason, description, "local", now, automaticSecurityBlockTTL(previous.Violations+1))
	block.Domain = cleanSecurityText(domain, 255)
	blocks[ip] = block
	return block
}

func securityCategoryBlocks(category string) bool {
	return securityCategoryThreshold(category) > 0
}

// High-confidence probes can block immediately. Heuristic categories need
// repeated evidence inside one minute to avoid false positives from a single
// malformed request, shared NAT/VPN address, or spoofed User-Agent.
func securityCategoryThreshold(category string) int {
	switch category {
	case "traversal", "repository", "secret", "enumeration":
		return 1
	case "injection":
		return 2
	case "source-backup", "scanner-client":
		return 3
	default:
		return 0
	}
}

func automaticSecurityBlockTTL(violation int) time.Duration {
	if violation < 1 {
		violation = 1
	}
	if violation > 6 {
		violation = 6
	}
	return time.Duration(1<<(violation-1)) * 24 * time.Hour
}

func blockSecurityIP(blocks map[string]SecurityBlock, ip, reason, description, source string, now time.Time, ttl time.Duration) SecurityBlock {
	previous := blocks[ip]
	violations := previous.Violations + 1
	if violations < 1 {
		violations = 1
	}
	if ttl < time.Minute {
		ttl = defaultSecurityBlockTTL
	}
	incidentID := previous.IncidentID
	if incidentID == "" {
		incidentID = randomIncidentID()
	}
	cleanReason := cleanSecurityText(reason, 96)
	cleanDescription := cleanSecurityText(description, 240)
	cleanSource := cleanSecurityText(source, 32)
	reasonLog := append([]SecurityReasonEvent(nil), previous.ReasonLog...)
	reasonLog = appendSecurityReasonEvent(reasonLog, SecurityReasonEvent{First: now, At: now, Reason: cleanReason, Description: cleanDescription, Source: cleanSource, Count: 1}, now)
	block := SecurityBlock{
		IP:          ip,
		Reason:      cleanReason,
		Description: cleanDescription,
		LastEvent:   now,
		Source:      cleanSource,
		ExpiresAt:   now.Add(ttl),
		Violations:  violations,
		IncidentID:  incidentID,
		ReasonLog:   reasonLog,
	}
	blocks[ip] = block
	return block
}

func appendSecurityReasonEvent(events []SecurityReasonEvent, event SecurityReasonEvent, now time.Time) []SecurityReasonEvent {
	events = pruneSecurityReasonLog(events, now)
	cutoff := now.Add(-securityReasonHistoryTTL)
	for index := len(events) - 1; index >= 0; index-- {
		previous := &events[index]
		if previous.Reason != event.Reason || previous.Description != event.Description || previous.Source != event.Source {
			continue
		}
		if previous.First.IsZero() {
			previous.First = previous.At
		}
		if previous.First.Before(cutoff) {
			events = append(events[:index], events[index+1:]...)
			break
		}
		if previous.Count < 1 {
			previous.Count = 1
		}
		previous.At = event.At
		previous.Count++
		return events
	}
	if event.Count < 1 {
		event.Count = 1
	}
	if event.First.IsZero() {
		event.First = event.At
	}
	events = append(events, event)
	if len(events) > securityReasonLogLimit {
		events = append([]SecurityReasonEvent(nil), events[len(events)-securityReasonLogLimit:]...)
	}
	return events
}

func pruneSecurityReasonLog(events []SecurityReasonEvent, now time.Time) []SecurityReasonEvent {
	cutoff := now.Add(-securityReasonHistoryTTL)
	kept := events[:0]
	for _, event := range events {
		if event.At.Before(cutoff) {
			continue
		}
		if event.Count < 1 {
			event.Count = 1
		}
		if event.First.IsZero() {
			event.First = event.At
		}
		kept = append(kept, event)
	}
	if len(kept) > securityReasonLogLimit {
		kept = kept[len(kept)-securityReasonLogLimit:]
	}
	return kept
}

func evictOldestAttackWindow(windows map[string]*attackWindow) {
	oldestKey := ""
	oldestStarted := time.Time{}
	for key, window := range windows {
		if window == nil {
			delete(windows, key)
			return
		}
		if oldestKey == "" || window.Started.Before(oldestStarted) {
			oldestKey = key
			oldestStarted = window.Started
		}
	}
	if oldestKey != "" {
		delete(windows, oldestKey)
	}
}

func evictOldestIncidentWindow(incidents map[string]*incidentWindow) {
	oldestKey := ""
	oldestStarted := time.Time{}
	for key, window := range incidents {
		if window == nil {
			delete(incidents, key)
			return
		}
		if oldestKey == "" || window.Started.Before(oldestStarted) {
			oldestKey = key
			oldestStarted = window.Started
		}
	}
	if oldestKey != "" {
		delete(incidents, oldestKey)
	}
}

func pruneAttackGuardState(blocks map[string]SecurityBlock, windows map[string]*attackWindow, incidents map[string]*incidentWindow, now time.Time) {
	for ip, block := range blocks {
		if !block.LastEvent.IsZero() && now.Sub(block.LastEvent) > securityEscalationHistoryTTL {
			delete(blocks, ip)
			continue
		}
		block.ReasonLog = pruneSecurityReasonLog(block.ReasonLog, now)
		blocks[ip] = block
	}
	for ip, window := range windows {
		if now.Sub(window.Started) > time.Minute {
			delete(windows, ip)
		}
	}
	for ip, window := range incidents {
		if now.Sub(window.Started) > time.Minute {
			delete(incidents, ip)
		}
	}
}

func (guard *AttackGuard) Check(ip string, now time.Time) (SecurityBlock, bool) {
	result, ok := guard.exchangeFast(attackGuardRequest{Operation: attackGuardCheck, IP: ip, Now: now})
	return result.Block, ok && result.Blocked
}

func (guard *AttackGuard) CheckSite(domain, ip string, now time.Time) (SecurityBlock, bool) {
	result, ok := guard.exchangeFast(attackGuardRequest{Operation: attackGuardCheck, Domain: normalizeSecurityDomain(domain), IP: ip, Now: now})
	return result.Block, ok && result.Blocked
}

func (guard *AttackGuard) ObserveFast(ip, path string, trusted bool, now time.Time) (SecurityBlock, bool) {
	return guard.ObserveRequestFast(ip, path, "GET", trusted, now)
}

func (guard *AttackGuard) ObserveRequestFast(ip, path, method string, trusted bool, now time.Time) (SecurityBlock, bool) {
	block, blocked, _ := guard.ObserveRequestFastDisposition(ip, path, method, trusted, now)
	return block, blocked
}

func (guard *AttackGuard) ObserveRequestFastDisposition(ip, path, method string, trusted bool, now time.Time) (SecurityBlock, bool, bool) {
	block, blocked, allowed := guard.ObserveSiteRequestFastDisposition("", ip, path, method, trusted, now)
	if blocked || allowed || trusted {
		return block, blocked, allowed
	}
	if method == "GET" || method == "HEAD" {
		return guard.ObserveSiteNotFoundFastDisposition("", ip, path, now)
	}
	return block, blocked, allowed
}

func (guard *AttackGuard) ObserveSiteRequestFastDisposition(domain, ip, path, method string, trusted bool, now time.Time) (SecurityBlock, bool, bool) {
	result, ok := guard.exchangeFast(attackGuardRequest{Operation: attackGuardObserveFast, Domain: normalizeSecurityDomain(domain), IP: ip, Path: path, Method: method, Trusted: trusted, Now: now})
	if ok && result.Changed {
		guard.signalSave()
	}
	return result.Block, ok && result.Blocked, ok && result.Allowed
}

// ObserveSiteNotFoundFastDisposition counts only misses so normal page traffic
// cannot be mistaken for a path scan merely because it uses many unique URLs.
func (guard *AttackGuard) ObserveSiteNotFoundFastDisposition(domain, ip, path string, now time.Time) (SecurityBlock, bool, bool) {
	result, ok := guard.exchangeFast(attackGuardRequest{
		Operation:  attackGuardObserveFast,
		Domain:     normalizeSecurityDomain(domain),
		IP:         ip,
		Path:       path,
		Method:     "GET",
		StatusCode: http.StatusNotFound,
		Now:        now,
	})
	if ok && result.Changed {
		guard.signalSave()
	}
	return result.Block, ok && result.Blocked, ok && result.Allowed
}

func (guard *AttackGuard) ObserveIncident(ip, category, description string, now time.Time) (SecurityBlock, bool) {
	return guard.ObserveSiteIncident("", ip, category, description, now)
}

func (guard *AttackGuard) ObserveSiteIncident(domain, ip, category, description string, now time.Time) (SecurityBlock, bool) {
	result, ok := guard.exchangeFast(attackGuardRequest{Operation: attackGuardObserveIncident, Domain: normalizeSecurityDomain(domain), IP: ip, Category: category, Description: description, Now: now})
	if ok && result.Changed {
		guard.signalSave()
	}
	return result.Block, ok && result.Blocked
}

func (guard *AttackGuard) Add(ip, reason, description, source string, expiresAt, now time.Time) (SecurityBlock, error) {
	result, ok := guard.exchangeAdmin(attackGuardRequest{Operation: attackGuardAdd, IP: ip, Reason: reason, Description: description, Source: source, ExpiresAt: expiresAt, Now: now})
	if !ok {
		return SecurityBlock{}, errors.New("security guard is busy")
	}
	if result.Changed {
		guard.signalSave()
	}
	return result.Block, result.Err
}

func (guard *AttackGuard) Update(ip, reason, description string) error {
	result, ok := guard.exchangeAdmin(attackGuardRequest{Operation: attackGuardUpdate, IP: ip, Reason: reason, Description: description, Now: time.Now().UTC()})
	if !ok {
		return errors.New("security guard is busy")
	}
	if result.Changed {
		guard.signalSave()
	}
	return result.Err
}

func (guard *AttackGuard) Remove(ip string) error {
	result, ok := guard.exchangeAdmin(attackGuardRequest{Operation: attackGuardRemove, IP: ip, Now: time.Now().UTC()})
	if !ok {
		return errors.New("security guard is busy")
	}
	if result.Changed {
		guard.signalSave()
	}
	return result.Err
}

func (guard *AttackGuard) ClaimIncidentReport(ip, incidentID string, now time.Time) (SecurityBlock, bool, error) {
	result, ok := guard.exchangeAdmin(attackGuardRequest{Operation: attackGuardClaimIncidentReport, IP: ip, Reason: incidentID, Now: now})
	if !ok {
		return SecurityBlock{}, false, errors.New("security guard is busy")
	}
	return result.Block, result.Allowed, result.Err
}

func (guard *AttackGuard) FinishIncidentReport(ip, incidentID, message string, success bool, now time.Time) (SecurityBlock, error) {
	result, ok := guard.exchangeAdmin(attackGuardRequest{
		Operation:   attackGuardFinishIncidentReport,
		IP:          ip,
		Reason:      incidentID,
		Description: message,
		Success:     success,
		Now:         now,
	})
	if !ok {
		return SecurityBlock{}, errors.New("security guard is busy")
	}
	if result.Changed {
		guard.signalSave()
	}
	return result.Block, result.Err
}

func (guard *AttackGuard) ApplyGlobal(ip, reason, description string, lastEvent, expiresAt time.Time) error {
	result, ok := guard.exchangeAdmin(attackGuardRequest{
		Operation:   attackGuardApplyGlobal,
		IP:          ip,
		Reason:      reason,
		Description: description,
		Now:         lastEvent,
		ExpiresAt:   expiresAt,
	})
	if !ok {
		return errors.New("security guard is busy")
	}
	if result.Changed {
		guard.signalSave()
	}
	return result.Err
}

func (guard *AttackGuard) IsAllowed(ip string) bool {
	result, ok := guard.exchangeAdmin(attackGuardRequest{Operation: attackGuardAllowCheck, IP: ip, Now: time.Now().UTC()})
	return ok && result.Allowed
}

func (guard *AttackGuard) Allow(ip, comment string) error {
	result, ok := guard.exchangeAdmin(attackGuardRequest{Operation: attackGuardAllowAdd, IP: ip, Comment: comment, Now: time.Now().UTC()})
	if !ok {
		return errors.New("security guard is busy")
	}
	if result.Changed {
		guard.signalSave()
	}
	return result.Err
}

func (guard *AttackGuard) Disallow(ip string) error {
	result, ok := guard.exchangeAdmin(attackGuardRequest{Operation: attackGuardAllowRemove, IP: ip, Now: time.Now().UTC()})
	if !ok {
		return errors.New("security guard is busy")
	}
	if result.Changed {
		guard.signalSave()
	}
	return result.Err
}

// TrustAdminIP applies a recently authenticated administrator address only to one site.
func (guard *AttackGuard) TrustAdminIP(domain, ip string, now time.Time) error {
	domain = normalizeSecurityDomain(domain)
	ip = normalizeSecurityIP(ip)
	if domain == "" || ip == "" {
		return errors.New("domain and valid IP are required")
	}
	result, ok := guard.exchangeFast(attackGuardRequest{
		Operation: attackGuardTrustAdminIP,
		Domain:    domain,
		IP:        ip,
		Now:       now,
	})
	if !ok {
		return errors.New("security guard is busy")
	}
	return result.Err
}

func (guard *AttackGuard) AdminIPTrusted(domain, ip string, now time.Time) bool {
	result, ok := guard.exchangeFast(attackGuardRequest{
		Operation: attackGuardCheckAdminIP,
		Domain:    normalizeSecurityDomain(domain),
		IP:        ip,
		Now:       now,
	})
	return ok && result.Allowed
}

// ClaimAdminIPLookup bounds database checks for repeated blocked requests.
func (guard *AttackGuard) ClaimAdminIPLookup(domain, ip string, now time.Time) bool {
	result, ok := guard.exchangeFast(attackGuardRequest{
		Operation: attackGuardClaimAdminIPLookup,
		Domain:    normalizeSecurityDomain(domain),
		IP:        ip,
		Now:       now,
	})
	return ok && result.Allowed
}

func normalizeSecurityDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
}

func (guard *AttackGuard) Allowlist() ([]SecurityAllow, error) {
	entries := []SecurityAllow{}
	for index := range guard.shards {
		result, ok := guard.exchangeShardAdmin(index, attackGuardRequest{Operation: attackGuardAllowSnapshot, Now: time.Now().UTC()})
		if !ok {
			return nil, errors.New("security guard is busy")
		}
		if result.Err != nil {
			return nil, result.Err
		}
		entries = append(entries, result.Allowlist...)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].IP < entries[j].IP })
	return entries, nil
}

func (guard *AttackGuard) Settings() (SecuritySettings, error) {
	result, ok := guard.exchangeAdmin(attackGuardRequest{Operation: attackGuardGetSettings, Now: time.Now().UTC()})
	if !ok {
		return SecuritySettings{}, errors.New("security guard is busy")
	}
	return result.Settings, result.Err
}

func (guard *AttackGuard) SetSettings(settings SecuritySettings) error {
	for index := range guard.shards {
		result, ok := guard.exchangeShardAdmin(index, attackGuardRequest{Operation: attackGuardSetSettings, Settings: settings, Now: time.Now().UTC()})
		if !ok {
			return errors.New("security guard is busy")
		}
		if result.Err != nil {
			return result.Err
		}
	}
	guard.signalSave()
	return nil
}

func (guard *AttackGuard) Snapshot(now time.Time) ([]SecurityBlock, error) {
	blocks := []SecurityBlock{}
	for index := range guard.shards {
		result, ok := guard.exchangeShardAdmin(index, attackGuardRequest{Operation: attackGuardSnapshot, Now: now})
		if !ok {
			return nil, errors.New("security guard is busy")
		}
		if result.Err != nil {
			return nil, result.Err
		}
		blocks = append(blocks, result.Blocks...)
	}
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].LastEvent.Equal(blocks[j].LastEvent) {
			return blocks[i].IP < blocks[j].IP
		}
		return blocks[i].LastEvent.After(blocks[j].LastEvent)
	})
	return blocks, nil
}

func (guard *AttackGuard) persistenceSnapshot(now time.Time) ([]SecurityBlock, error) {
	blocks := []SecurityBlock{}
	for index := range guard.shards {
		result, ok := guard.exchangeShardAdmin(index, attackGuardRequest{Operation: attackGuardPersistenceSnapshot, Now: now})
		if !ok {
			return nil, errors.New("security guard is busy")
		}
		if result.Err != nil {
			return nil, result.Err
		}
		blocks = append(blocks, result.Blocks...)
	}
	return blocks, nil
}

func (guard *AttackGuard) exchangeFast(request attackGuardRequest) (attackGuardResult, bool) {
	ip := normalizeSecurityIP(request.IP)
	if ip == "" {
		return attackGuardResult{}, true
	}
	request.IP = ip
	index := attackGuardShardIndex(ip)
	reply := make(chan attackGuardResult, 1)
	request.Reply = reply

	select {
	case <-guard.done:
		return attackGuardResult{}, false
	case guard.shards[index] <- request:
	default:
		return attackGuardResult{}, false
	}
	select {
	case <-guard.done:
		return attackGuardResult{}, false
	case result := <-reply:
		return result, true
	}
}

func (guard *AttackGuard) exchangeAdmin(request attackGuardRequest) (attackGuardResult, bool) {
	ip := normalizeSecurityIP(request.IP)
	if ip != "" {
		request.IP = ip
		return guard.exchangeShardAdmin(attackGuardShardIndex(ip), request)
	}
	return guard.exchangeShardAdmin(0, request)
}

func (guard *AttackGuard) exchangeShardAdmin(index int, request attackGuardRequest) (attackGuardResult, bool) {
	reply := make(chan attackGuardResult, 1)
	request.Reply = reply
	select {
	case <-guard.done:
		return attackGuardResult{}, false
	case guard.shards[index] <- request:
	default:
		return attackGuardResult{}, false
	}
	select {
	case <-guard.done:
		return attackGuardResult{}, false
	case result := <-reply:
		return result, true
	}
}

func attackGuardShardIndex(ip string) int {
	var hash uint32 = 2166136261
	for index := 0; index < len(ip); index++ {
		hash ^= uint32(ip[index])
		hash *= 16777619
	}
	return int(hash % attackGuardShardCount)
}

func (guard *AttackGuard) signalSave() {
	select {
	case guard.save <- struct{}{}:
	default:
	}
}

func (guard *AttackGuard) runPersistence() {
	defer close(guard.done)
	var pending bool
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	for {
		select {
		case <-guard.shutdown:
			return
		case <-guard.save:
			if pending {
				continue
			}
			pending = true
			timer.Reset(time.Second)
		case <-timer.C:
			_ = guard.saveSnapshot()
			pending = false
		}
	}
}

func (guard *AttackGuard) saveSnapshot() error {
	if strings.TrimSpace(guard.path) == "" {
		return nil
	}
	settings, err := guard.Settings()
	if err != nil {
		return err
	}
	blocks, err := guard.persistenceSnapshot(time.Now().UTC())
	if err != nil {
		return err
	}
	allowlist, err := guard.Allowlist()
	if err != nil {
		return err
	}
	state := attackGuardDiskState{Version: 2, Settings: settings, Blocks: blocks, Allowlist: allowlist}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(guard.path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(guard.path), ".security-*")
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
	return os.Rename(temporaryPath, guard.path)
}

func (guard *AttackGuard) Close() {
	_ = guard.saveSnapshot()
	close(guard.shutdown)
	<-guard.done
}

func BlockedHTML(language string, block SecurityBlock) string {
	return blockedHTMLAt(language, block, time.Now().UTC())
}

func blockedHTMLAt(language string, block SecurityBlock, now time.Time) string {
	title := "Request blocked"
	message := "SiteBrush temporarily blocked requests from this address."
	support := "You can find this incident by its ID in Analytics → Security."
	ipLabel := "Client address"
	causeLabel := "Reason for blocking"
	sourceLabel := "Blocking source"
	untilLabel := "Blocked until (UTC)"
	durationLabel := "Block duration"
	remainingLabel := "remaining"
	eventsLabel := "Events that triggered the block"
	reportLabel := "Think this block is a mistake?"
	reportHelp := "Send this incident to the site administrator once. Add a short explanation if useful."
	reportButton := "Send incident to administrator"
	reportPlaceholder := "Short explanation (optional)"
	reportSent := "This incident has already been sent to the site administrator."
	developerLabel := "For developers testing locally"
	developerHelp := "Run tests against httptest servers or mocked HTTP transports. A local go test should not send repeated authentication requests to sitebrush.com. When testing a local SiteBrush server, use localhost; in a local development installation, disable Auto-block and Global reputation sync in Analytics → Security, or ask an administrator to allowlist your client address. Do not disable protection on the public server."
	language = strings.ToLower(language)
	if strings.HasPrefix(language, "ru") {
		title = "Запрос заблокирован"
		message = "SiteBrush временно заблокировал запросы с этого адреса."
		support = "Администратор может найти этот инцидент по ID в разделе «Аналитика → Безопасность»."
		ipLabel = "Заблокированный IP-адрес"
		causeLabel = "Причина блокировки"
		sourceLabel = "Источник блокировки"
		untilLabel = "Блокировка действует до (UTC)"
		durationLabel = "Срок блокировки"
		remainingLabel = "осталось"
		eventsLabel = "Действия, вызвавшие блокировку"
		reportLabel = "Считаете блокировку ошибочной?"
		reportHelp = "Один раз отправьте этот инцидент администратору сайта. При необходимости добавьте короткое пояснение."
		reportButton = "Отправить инцидент администратору"
		reportPlaceholder = "Короткое пояснение (необязательно)"
		reportSent = "Информация об этом инциденте уже отправлена администратору сайта."
		developerLabel = "Для разработчиков: безопасное тестирование"
		developerHelp = "Запускайте тесты с httptest-серверами или подменёнными HTTP-транспортами. Локальный go test не должен отправлять повторные запросы аутентификации на sitebrush.com. Для проверки локального SiteBrush используйте localhost; в локальной среде разработки отключите Auto-block и Global reputation sync в разделе Analytics → Security либо попросите администратора добавить ваш адрес в список разрешённых. Не отключайте защиту на публичном сервере."
	} else if strings.HasPrefix(language, "de") {
		title = "Anfrage blockiert"
		message = "SiteBrush hat Anfragen von dieser Adresse vorübergehend blockiert."
		support = "Der Administrator kann diesen Vorfall über seine ID unter Analytics → Security finden."
		ipLabel = "Gesperrte IP-Adresse"
		causeLabel = "Grund der Sperre"
		sourceLabel = "Sperrquelle"
		untilLabel = "Gesperrt bis (UTC)"
		durationLabel = "Sperrdauer"
		remainingLabel = "verbleibend"
		eventsLabel = "Ereignisse, die zur Sperre geführt haben"
		reportLabel = "Halten Sie die Sperre für einen Fehler?"
		reportHelp = "Senden Sie diesen Vorfall einmalig an den Administrator. Eine kurze Erklärung ist optional."
		reportButton = "Vorfall an Administrator senden"
		reportPlaceholder = "Kurze Erklärung (optional)"
		reportSent = "Dieser Vorfall wurde bereits an den Administrator gesendet."
		developerLabel = "Für Entwickler: sicher lokal testen"
		developerHelp = "Tests sollten httptest-Server oder gemockte HTTP-Transporte verwenden. Ein lokales go test sollte keine wiederholten Authentifizierungsanfragen an sitebrush.com senden. Verwenden Sie localhost für lokale SiteBrush-Tests. Deaktivieren Sie Auto-block und Global reputation sync nur in einer lokalen Entwicklungsinstallation oder lassen Sie Ihre Adresse freischalten. Deaktivieren Sie den Schutz nicht auf einem öffentlichen Server."
	}
	var body strings.Builder
	body.WriteString("<!doctype html><meta charset=utf-8><meta name=viewport content=\"width=device-width\"><title>")
	body.WriteString(html.EscapeString(title))
	body.WriteString("</title><main><h1>")
	body.WriteString(html.EscapeString(title))
	body.WriteString("</h1><p>")
	body.WriteString(html.EscapeString(message))
	body.WriteString("</p><p>Incident: <code>")
	body.WriteString(html.EscapeString(block.IncidentID))
	body.WriteString("</code></p><p>")
	body.WriteString(html.EscapeString(support))
	body.WriteString("</p><dl><dt>")
	body.WriteString(html.EscapeString(causeLabel))
	body.WriteString("</dt><dd><strong>")
	body.WriteString(html.EscapeString(securityReasonLabel(block.Reason, language)))
	body.WriteString("</strong>")
	if block.Description != "" {
		body.WriteString("<div>")
		body.WriteString(html.EscapeString(localizeSecurityDescription(block.Reason, block.Description, language)))
		body.WriteString("</div>")
	}
	body.WriteString("</dd><dt>")
	body.WriteString(html.EscapeString(ipLabel))
	body.WriteString("</dt><dd><code>")
	body.WriteString(html.EscapeString(block.IP))
	body.WriteString("</code></dd><dt>")
	body.WriteString(html.EscapeString(sourceLabel))
	body.WriteString("</dt><dd>")
	body.WriteString(html.EscapeString(securitySourceLabel(block.Source, language)))
	body.WriteString("</dd><dt>")
	body.WriteString(html.EscapeString(durationLabel))
	body.WriteString("</dt><dd>")
	if !block.LastEvent.IsZero() && !block.ExpiresAt.IsZero() {
		body.WriteString(html.EscapeString(formatSecurityDuration(block.ExpiresAt.Sub(block.LastEvent), language)))
	} else {
		body.WriteString("—")
	}
	body.WriteString("</dd><dt>")
	body.WriteString(html.EscapeString(untilLabel))
	body.WriteString("</dt><dd>")
	if !block.ExpiresAt.IsZero() {
		body.WriteString(html.EscapeString(block.ExpiresAt.UTC().Format(time.RFC3339)))
		body.WriteString(" (")
		body.WriteString(html.EscapeString(formatSecurityDuration(block.ExpiresAt.Sub(now), language)))
		body.WriteString(" ")
		body.WriteString(html.EscapeString(remainingLabel))
		body.WriteString(")")
	} else {
		body.WriteString("—")
	}
	body.WriteString("</dd></dl><h2>")
	body.WriteString(html.EscapeString(eventsLabel))
	body.WriteString("</h2><ul>")
	if len(block.ReasonLog) == 0 {
		body.WriteString("<li>")
		body.WriteString(html.EscapeString(localizeSecurityDescription(block.Reason, block.Description, language)))
		if !block.LastEvent.IsZero() {
			body.WriteString(" — ")
			body.WriteString(html.EscapeString(block.LastEvent.UTC().Format(time.RFC3339)))
		}
		body.WriteString("</li>")
	} else {
		for _, event := range block.ReasonLog {
			body.WriteString("<li><time>")
			if !event.First.IsZero() && !event.First.Equal(event.At) {
				body.WriteString(html.EscapeString(event.First.UTC().Format(time.RFC3339)))
				body.WriteString(" – ")
			}
			body.WriteString(html.EscapeString(event.At.UTC().Format(time.RFC3339)))
			body.WriteString("</time> — <strong>")
			body.WriteString(html.EscapeString(securityReasonLabel(event.Reason, language)))
			body.WriteString("</strong>: ")
			body.WriteString(html.EscapeString(localizeSecurityDescription(event.Reason, event.Description, language)))
			if event.Count > 1 {
				body.WriteString(" (×")
				body.WriteString(fmt.Sprint(event.Count))
				body.WriteString(")")
			}
			if event.Source != "" {
				body.WriteString(" [")
				body.WriteString(html.EscapeString(securitySourceLabel(event.Source, language)))
				body.WriteString("]")
			}
			body.WriteString("</li>")
		}
	}
	body.WriteString("</ul><h2>")
	body.WriteString(html.EscapeString(reportLabel))
	body.WriteString("</h2>")
	if !block.ReportedAt.IsZero() {
		body.WriteString("<p>")
		body.WriteString(html.EscapeString(reportSent))
		body.WriteString("</p>")
	} else {
		body.WriteString("<p>")
		body.WriteString(html.EscapeString(reportHelp))
		body.WriteString("</p><form method=\"post\" action=\"?security_incident_report\"><input type=\"hidden\" name=\"incident_id\" value=\"")
		body.WriteString(html.EscapeString(block.IncidentID))
		body.WriteString("\"><textarea name=\"message\" maxlength=\"500\" rows=\"3\" placeholder=\"")
		body.WriteString(html.EscapeString(reportPlaceholder))
		body.WriteString("\"></textarea><br><button type=\"submit\">")
		body.WriteString(html.EscapeString(reportButton))
		body.WriteString("</button></form>")
	}
	body.WriteString("<h2>")
	body.WriteString(html.EscapeString(developerLabel))
	body.WriteString("</h2><p>")
	body.WriteString(html.EscapeString(developerHelp))
	body.WriteString("</p></main>")
	return body.String()
}

func formatSecurityDuration(duration time.Duration, language string) string {
	if duration < 0 {
		duration = 0
	}
	minutes := int(duration / time.Minute)
	if duration%time.Minute > 0 {
		minutes++
	}
	days := minutes / (24 * 60)
	hours := minutes % (24 * 60) / 60
	remainingMinutes := minutes % 60
	if strings.HasPrefix(language, "ru") {
		if days > 0 {
			return fmt.Sprintf("%d дн. %d ч.", days, hours)
		}
		if hours > 0 {
			return fmt.Sprintf("%d ч. %d мин.", hours, remainingMinutes)
		}
		return fmt.Sprintf("%d мин.", remainingMinutes)
	}
	if days > 0 {
		return fmt.Sprintf("%d d %d h", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%d h %d min", hours, remainingMinutes)
	}
	return fmt.Sprintf("%d min", remainingMinutes)
}

func securityReasonLabel(reason, language string) string {
	if strings.HasPrefix(language, "ru") {
		switch reason {
		case "authentication-failures":
			return "Повторные ошибки аутентификации"
		case "repository":
			return "Попытка доступа к служебным файлам репозитория"
		case "secret":
			return "Попытка доступа к секретному или конфигурационному файлу"
		case "source-backup":
			return "Попытка скачать резервную копию или исходный файл"
		case "scanner-client":
			return "Обнаружен клиент, похожий на сканер уязвимостей"
		case "injection":
			return "Обнаружен шаблон инъекции"
		case "traversal":
			return "Попытка обхода пути"
		case "enumeration", "mass-enumeration":
			return "Сканирование множества адресов"
		case "mass-write":
			return "Слишком много запросов на изменение"
		}
	}
	if strings.HasPrefix(language, "de") {
		switch reason {
		case "authentication-failures":
			return "Wiederholte Authentifizierungsfehler"
		case "repository":
			return "Zugriff auf Repository-Metadaten"
		case "secret":
			return "Zugriff auf eine sensible Datei"
		case "source-backup":
			return "Versuch, Backup- oder Quelldatei herunterzuladen"
		case "scanner-client":
			return "Client ähnelt einem Schwachstellen-Scanner"
		case "injection":
			return "Einschleusungsmuster erkannt"
		case "traversal":
			return "Pfadumgehungsversuch"
		case "enumeration", "mass-enumeration":
			return "Scan vieler Adressen"
		case "mass-write":
			return "Zu viele Änderungsanfragen"
		}
	}
	return reason
}

func securitySourceLabel(source, language string) string {
	if strings.HasPrefix(language, "ru") {
		switch source {
		case "local":
			return "Обнаружено этим сервером"
		case "global":
			return "Глобальная репутация: подтверждено несколькими серверами SiteBrush"
		case "manual":
			return "Добавлено администратором вручную"
		}
	}
	if strings.HasPrefix(language, "de") {
		switch source {
		case "local":
			return "Von diesem Server erkannt"
		case "global":
			return "Globale Reputation: von mehreren SiteBrush-Servern bestätigt"
		case "manual":
			return "Manuell vom Administrator hinzugefügt"
		}
	}
	switch source {
	case "local":
		return "Detected by this server"
	case "global":
		return "Global reputation: confirmed by multiple SiteBrush servers"
	case "manual":
		return "Added manually by the administrator"
	default:
		return source
	}
}

func localizeSecurityDescription(reason, description, language string) string {
	if reason != "authentication-failures" {
		return description
	}
	const prefix = "Repeated authentication failures; latest request: "
	if !strings.HasPrefix(description, prefix) {
		return description
	}
	request := strings.TrimPrefix(description, prefix)
	if strings.HasPrefix(language, "ru") {
		return "Отклонённая попытка входа; последний запрос: " + request
	}
	if strings.HasPrefix(language, "de") {
		return "Abgewiesener Anmeldeversuch; letzte Anfrage: " + request
	}
	return description
}

func normalizeSecurityIP(value string) string {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return ""
	}
	return ip.String()
}

func boundedPath(value string) string {
	if len(value) > 256 {
		return value[:256]
	}
	return value
}

func cleanSecurityText(value string, limit int) string {
	value = strings.TrimSpace(strings.Map(func(character rune) rune {
		if character < ' ' {
			return -1
		}
		return character
	}, value))
	if len(value) > limit {
		return value[:limit]
	}
	return value
}

func randomIncidentID() string {
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000")))
	}
	return hex.EncodeToString(randomBytes[:])
}
