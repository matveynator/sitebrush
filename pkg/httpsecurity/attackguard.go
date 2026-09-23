package httpsecurity

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultSecurityBlockTTL = 7 * 24 * time.Hour
	securityReasonHistoryTTL = 7 * 24 * time.Hour
	securityReasonLogLimit   = 24
	attackGuardShardCount    = 16
	attackGuardQueueSize     = 256
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
	IP          string                `json:"ip"`
	Reason      string                `json:"reason"`
	Description string                `json:"description"`
	LastEvent   time.Time             `json:"last_event"`
	Source      string                `json:"source"`
	ExpiresAt   time.Time             `json:"expires_at"`
	Violations  int                   `json:"violations"`
	IncidentID  string                `json:"incident_id"`
	ReasonLog   []SecurityReasonEvent `json:"reason_log,omitempty"`
}

type SecurityAllow struct {
	IP      string    `json:"ip"`
	Comment string    `json:"comment"`
	AddedAt time.Time `json:"added_at"`
}

type attackWindow struct {
	Started   time.Time
	Count     int
	PostCount int
	Distinct  map[string]struct{}
}

type attackGuardDiskState struct {
	Version   int               `json:"version"`
	Settings  SecuritySettings  `json:"settings"`
	Blocks    []SecurityBlock   `json:"blocks"`
	Allowlist []SecurityAllow   `json:"allowlist,omitempty"`
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
	attackGuardGetSettings
	attackGuardSetSettings
	attackGuardApplyGlobal
	attackGuardAllowAdd
	attackGuardAllowRemove
	attackGuardAllowCheck
	attackGuardAllowSnapshot
)

type attackGuardRequest struct {
	Operation   attackGuardOperation
	IP          string
	Path        string
	Method      string
	Category    string
	Reason      string
	Description string
	Source      string
	Trusted     bool
	Now         time.Time
	ExpiresAt   time.Time
	Settings    SecuritySettings
	Comment     string
	Reply       chan attackGuardResult
}

type attackGuardResult struct {
	Block     SecurityBlock
	Blocked   bool
	Blocks    []SecurityBlock
	Allowed   bool
	Allowlist []SecurityAllow
	Settings  SecuritySettings
	Changed  bool
	Err      error
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
	return state, nil
}

func runAttackGuardShard(requests <-chan attackGuardRequest, shutdown <-chan struct{}, settings SecuritySettings, initial []SecurityBlock, initialAllowlist []SecurityAllow) {
	blocks := make(map[string]SecurityBlock, len(initial))
	allowlist := make(map[string]SecurityAllow, len(initialAllowlist))
	windows := map[string]*attackWindow{}
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
			pruneAttackGuardState(blocks, windows, now.UTC())
		case request := <-requests:
			result := handleAttackGuardRequest(blocks, allowlist, windows, &settings, request)
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

func handleAttackGuardRequest(blocks map[string]SecurityBlock, allowlist map[string]SecurityAllow, windows map[string]*attackWindow, settings *SecuritySettings, request attackGuardRequest) attackGuardResult {
	now := request.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ip := normalizeSecurityIP(request.IP)

	switch request.Operation {
	case attackGuardCheck:
		if _, allowed := allowlist[ip]; allowed {
			return attackGuardResult{Allowed: true}
		}
		block, blocked := activeSecurityBlock(blocks, ip, now)
		return attackGuardResult{Block: block, Blocked: blocked}

	case attackGuardObserveFast:
		if _, allowed := allowlist[ip]; allowed {
			return attackGuardResult{Allowed: true}
		}
		if block, blocked := activeSecurityBlock(blocks, ip, now); blocked {
			return attackGuardResult{Block: block, Blocked: true}
		}
		if request.Trusted {
			return attackGuardResult{}
		}
		if !settings.AutoBlock || ip == "" {
			return attackGuardResult{}
		}
		window := windows[ip]
		if window == nil || now.Sub(window.Started) > 10*time.Second {
			window = &attackWindow{Started: now, Distinct: map[string]struct{}{}}
			windows[ip] = window
		}
		window.Count++
		switch strings.ToUpper(strings.TrimSpace(request.Method)) {
		case "POST", "PUT", "PATCH", "DELETE":
			window.PostCount++
		}
		if len(window.Distinct) < 256 {
			window.Distinct[boundedPath(request.Path)] = struct{}{}
		}
		if window.PostCount >= 120 || len(window.Distinct) >= 220 {
			reason := "mass-write"
			description := "abnormally high form, API, or page-generation rate"
			if len(window.Distinct) >= 220 {
				reason = "mass-enumeration"
				description = "abnormally high number of distinct paths"
			}
			block := blockSecurityIP(blocks, ip, reason, description, "local", now, defaultSecurityBlockTTL)
			delete(windows, ip)
			return attackGuardResult{Block: block, Blocked: true, Changed: true}
		}
		if len(windows) > 1024 {
			pruneAttackGuardState(blocks, windows, now)
		}
		return attackGuardResult{}

	case attackGuardObserveIncident:
		if _, allowed := allowlist[ip]; allowed {
			return attackGuardResult{Allowed: true}
		}
		if !settings.AutoBlock || ip == "" || !securityCategoryBlocks(request.Category) {
			return attackGuardResult{}
		}
		block := blockSecurityIP(blocks, ip, request.Category, request.Description, "local", now, defaultSecurityBlockTTL)
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
		return attackGuardResult{Changed: found}

	case attackGuardSnapshot:
		snapshot := make([]SecurityBlock, 0, len(blocks))
		for _, block := range blocks {
			if block.ExpiresAt.IsZero() || now.Before(block.ExpiresAt) {
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
		if found && existing.Source != "global" {
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
		reasonLog = append(reasonLog, SecurityReasonEvent{At: request.Now, Reason: cleanReason, Description: cleanDescription, Source: "global"})
		reasonLog = pruneSecurityReasonLog(reasonLog, now)
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
	}

	return attackGuardResult{Err: errors.New("unknown attack guard operation")}
}

func activeSecurityBlock(blocks map[string]SecurityBlock, ip string, now time.Time) (SecurityBlock, bool) {
	if ip == "" {
		return SecurityBlock{}, false
	}
	block, found := blocks[ip]
	if !found {
		return SecurityBlock{}, false
	}
	if !block.ExpiresAt.IsZero() && !now.Before(block.ExpiresAt) {
		delete(blocks, ip)
		return SecurityBlock{}, false
	}
	return block, true
}

func securityCategoryBlocks(category string) bool {
	switch category {
	case "injection", "traversal", "repository", "secret", "source-backup", "scanner-client", "enumeration", "authentication-failures":
		return true
	default:
		return false
	}
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
	if violations >= 3 && ttl < 30*24*time.Hour {
		ttl = 30 * 24 * time.Hour
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
	for index := len(events) - 1; index >= 0; index-- {
		previous := &events[index]
		if previous.Reason != event.Reason || previous.Description != event.Description || previous.Source != event.Source {
			continue
		}
		if previous.Count < 1 {
			previous.Count = 1
		}
		if previous.First.IsZero() {
			previous.First = previous.At
		}
		previous.At = event.At
		previous.Count++
		return events
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

func pruneAttackGuardState(blocks map[string]SecurityBlock, windows map[string]*attackWindow, now time.Time) {
	for ip, block := range blocks {
		if !block.ExpiresAt.IsZero() && !now.Before(block.ExpiresAt) {
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
}

func (guard *AttackGuard) Check(ip string, now time.Time) (SecurityBlock, bool) {
	result, ok := guard.exchangeFast(attackGuardRequest{Operation: attackGuardCheck, IP: ip, Now: now})
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
	result, ok := guard.exchangeFast(attackGuardRequest{Operation: attackGuardObserveFast, IP: ip, Path: path, Method: method, Trusted: trusted, Now: now})
	if ok && result.Changed {
		guard.signalSave()
	}
	return result.Block, ok && result.Blocked, ok && result.Allowed
}

func (guard *AttackGuard) ObserveIncident(ip, category, description string, now time.Time) (SecurityBlock, bool) {
	result, ok := guard.exchangeFast(attackGuardRequest{Operation: attackGuardObserveIncident, IP: ip, Category: category, Description: description, Now: now})
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
	blocks, err := guard.Snapshot(time.Now().UTC())
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
	title := "Request blocked"
	message := "SiteBrush temporarily blocked requests from this address."
	support := "Contact the site administrator and include the incident ID."
	language = strings.ToLower(language)
	if strings.HasPrefix(language, "ru") {
		title = "Запрос заблокирован"
		message = "SiteBrush временно заблокировал запросы с этого адреса."
		support = "Обратитесь к администратору сайта или support@sitebrush.com и укажите идентификатор инцидента."
	} else if strings.HasPrefix(language, "de") {
		title = "Anfrage blockiert"
		message = "SiteBrush hat Anfragen von dieser Adresse vorübergehend blockiert."
		support = "Kontaktieren Sie den Website-Administrator oder support@sitebrush.com und nennen Sie die Vorfall-ID."
	}
	return "<!doctype html><meta charset=utf-8><meta name=viewport content=\"width=device-width\"><title>" + html.EscapeString(title) + "</title><main><h1>" + html.EscapeString(title) + "</h1><p>" + html.EscapeString(message) + "</p><p>" + html.EscapeString(block.Reason) + "</p><p>Incident: <code>" + html.EscapeString(block.IncidentID) + "</code></p><p>" + html.EscapeString(support) + "</p></main>"
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
