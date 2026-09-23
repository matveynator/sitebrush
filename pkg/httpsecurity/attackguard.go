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
	"sync"
	"time"
)

const defaultSecurityBlockTTL = 7 * 24 * time.Hour

type SecuritySettings struct {
	AutoBlock  bool `json:"auto_block"`
	GlobalSync bool `json:"global_sync"`
}

type SecurityBlock struct {
	IP          string    `json:"ip"`
	Reason      string    `json:"reason"`
	Description string    `json:"description"`
	LastEvent   time.Time `json:"last_event"`
	Source      string    `json:"source"`
	ExpiresAt   time.Time `json:"expires_at"`
	Violations  int       `json:"violations"`
	IncidentID  string    `json:"incident_id"`
}

type attackWindow struct {
	Started  time.Time
	Count    int
	Distinct map[string]struct{}
}

type attackGuardDiskState struct {
	Settings SecuritySettings `json:"settings"`
	Blocks   []SecurityBlock  `json:"blocks"`
}

type AttackGuard struct {
	mu       sync.RWMutex
	path     string
	settings SecuritySettings
	blocks   map[string]SecurityBlock
	windows  map[string]*attackWindow
	dirty    bool
}

func NewAttackGuard(path string) *AttackGuard {
	return &AttackGuard{path: path, settings: SecuritySettings{AutoBlock: true}, blocks: map[string]SecurityBlock{}, windows: map[string]*attackWindow{}}
}

func LoadAttackGuard(path string) (*AttackGuard, error) {
	guard := NewAttackGuard(path)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) { return guard, nil }
	if err != nil { return guard, err }
	var state attackGuardDiskState
	if err := json.Unmarshal(data, &state); err != nil { return guard, err }
	guard.settings = state.Settings
	for _, block := range state.Blocks {
		if ip := normalizeSecurityIP(block.IP); ip != "" { block.IP = ip; guard.blocks[ip] = block }
	}
	guard.Prune(time.Now().UTC())
	guard.dirty = false
	return guard, nil
}

func (g *AttackGuard) Settings() SecuritySettings {
	g.mu.RLock(); defer g.mu.RUnlock(); return g.settings
}

func (g *AttackGuard) SetSettings(s SecuritySettings) {
	g.mu.Lock(); g.settings = s; g.dirty = true; g.mu.Unlock()
}

func (g *AttackGuard) Snapshot(now time.Time) []SecurityBlock {
	g.Prune(now)
	g.mu.RLock(); defer g.mu.RUnlock()
	out := make([]SecurityBlock, 0, len(g.blocks))
	for _, b := range g.blocks { out = append(out, b) }
	sort.Slice(out, func(i, j int) bool { return out[i].LastEvent.After(out[j].LastEvent) })
	return out
}

func (g *AttackGuard) Check(ip string, now time.Time) (SecurityBlock, bool) {
	ip = normalizeSecurityIP(ip)
	if ip == "" { return SecurityBlock{}, false }
	g.mu.RLock(); block, ok := g.blocks[ip]; g.mu.RUnlock()
	if !ok { return SecurityBlock{}, false }
	if !block.ExpiresAt.IsZero() && !now.Before(block.ExpiresAt) { g.Remove(ip); return SecurityBlock{}, false }
	return block, true
}

func (g *AttackGuard) ObserveFast(ip, path string, trusted bool, now time.Time) (SecurityBlock, bool) {
	if trusted { return g.Check(ip, now) }
	if block, ok := g.Check(ip, now); ok { return block, true }
	ip = normalizeSecurityIP(ip)
	if ip == "" { return SecurityBlock{}, false }
	g.mu.Lock(); defer g.mu.Unlock()
	if !g.settings.AutoBlock { return SecurityBlock{}, false }
	w := g.windows[ip]
	if w == nil || now.Sub(w.Started) > 10*time.Second {
		w = &attackWindow{Started: now, Distinct: map[string]struct{}{}}
		g.windows[ip] = w
	}
	w.Count++
	if len(w.Distinct) < 256 { w.Distinct[boundedPath(path)] = struct{}{} }
	if w.Count >= 600 || len(w.Distinct) >= 220 {
		reason, description := "dos", "abnormally high request rate"
		if len(w.Distinct) >= 220 { reason, description = "mass-enumeration", "abnormally high number of distinct paths" }
		block := g.blockLocked(ip, reason, description, "local", now, defaultSecurityBlockTTL)
		delete(g.windows, ip)
		return block, true
	}
	if len(g.windows) > 4096 { g.windows = map[string]*attackWindow{} }
	return SecurityBlock{}, false
}

func (g *AttackGuard) ObserveIncident(ip, category, description string, now time.Time) (SecurityBlock, bool) {
	ip = normalizeSecurityIP(ip)
	if ip == "" || category == "" { return SecurityBlock{}, false }
	g.mu.Lock(); defer g.mu.Unlock()
	if !g.settings.AutoBlock { return SecurityBlock{}, false }
	severe := map[string]bool{"injection": true, "traversal": true, "repository": true, "secret": true, "source-backup": true, "scanner-client": true, "enumeration": true, "authentication-failures": true, "rapid-crawl": true}
	if !severe[category] { return SecurityBlock{}, false }
	return g.blockLocked(ip, category, description, "local", now, defaultSecurityBlockTTL), true
}

func (g *AttackGuard) Add(ip, reason, description, source string, expiresAt, now time.Time) (SecurityBlock, error) {
	ip = normalizeSecurityIP(ip)
	if ip == "" { return SecurityBlock{}, errors.New("invalid IP address") }
	if source == "" { source = "manual" }
	if expiresAt.IsZero() { expiresAt = now.Add(defaultSecurityBlockTTL) }
	g.mu.Lock(); defer g.mu.Unlock()
	return g.blockLocked(ip, cleanSecurityText(reason, 96), cleanSecurityText(description, 240), source, now, expiresAt.Sub(now)), nil
}

func (g *AttackGuard) Update(ip, reason, description string) error {
	ip = normalizeSecurityIP(ip)
	g.mu.Lock(); defer g.mu.Unlock()
	b, ok := g.blocks[ip]
	if !ok { return errors.New("block not found") }
	b.Reason = cleanSecurityText(reason, 96)
	b.Description = cleanSecurityText(description, 240)
	g.blocks[ip] = b
	g.dirty = true
	return nil
}

func (g *AttackGuard) Remove(ip string) {
	ip = normalizeSecurityIP(ip)
	g.mu.Lock(); delete(g.blocks, ip); g.dirty = true; g.mu.Unlock()
}

func (g *AttackGuard) Prune(now time.Time) {
	g.mu.Lock(); defer g.mu.Unlock()
	for ip, b := range g.blocks {
		if !b.ExpiresAt.IsZero() && !now.Before(b.ExpiresAt) { delete(g.blocks, ip); g.dirty = true }
	}
	for ip, w := range g.windows {
		if now.Sub(w.Started) > time.Minute { delete(g.windows, ip) }
	}
}

func (g *AttackGuard) SaveIfDirty() error {
	g.mu.RLock()
	if !g.dirty { g.mu.RUnlock(); return nil }
	state := attackGuardDiskState{Settings: g.settings, Blocks: make([]SecurityBlock, 0, len(g.blocks))}
	for _, b := range g.blocks { state.Blocks = append(state.Blocks, b) }
	path := g.path
	g.mu.RUnlock()
	if path == "" { return nil }
	sort.Slice(state.Blocks, func(i, j int) bool { return state.Blocks[i].IP < state.Blocks[j].IP })
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil { return err }
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil { return err }
	tmp, err := os.CreateTemp(filepath.Dir(path), ".security-*")
	if err != nil { return err }
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err == nil { _, err = tmp.Write(data) }
	if closeErr := tmp.Close(); err == nil { err = closeErr }
	if err != nil { return err }
	if err = os.Rename(name, path); err != nil { return err }
	g.mu.Lock(); g.dirty = false; g.mu.Unlock()
	return nil
}

func (g *AttackGuard) blockLocked(ip, reason, description, source string, now time.Time, ttl time.Duration) SecurityBlock {
	previous, exists := g.blocks[ip]
	violations := 1
	if exists { violations = previous.Violations + 1 }
	if ttl < time.Minute { ttl = defaultSecurityBlockTTL }
	if violations >= 3 && ttl < 30*24*time.Hour { ttl = 30 * 24 * time.Hour }
	incident := previous.IncidentID
	if incident == "" { incident = randomIncidentID() }
	block := SecurityBlock{IP: ip, Reason: cleanSecurityText(reason, 96), Description: cleanSecurityText(description, 240), LastEvent: now, Source: source, ExpiresAt: now.Add(ttl), Violations: violations, IncidentID: incident}
	g.blocks[ip] = block
	g.dirty = true
	return block
}

func BlockedHTML(language string, block SecurityBlock) string {
	title, message, support := "Request blocked", "SiteBrush temporarily blocked requests from this address.", "Contact the site administrator and include the incident ID."
	lang := strings.ToLower(language)
	if strings.HasPrefix(lang, "ru") {
		title, message, support = "Запрос заблокирован", "SiteBrush временно заблокировал запросы с этого адреса.", "Обратитесь к администратору сайта и укажите идентификатор инцидента."
	} else if strings.HasPrefix(lang, "de") {
		title, message, support = "Anfrage blockiert", "SiteBrush hat Anfragen von dieser Adresse vorübergehend blockiert.", "Kontaktieren Sie den Website-Administrator und nennen Sie die Vorfall-ID."
	}
	return "<!doctype html><meta charset=utf-8><meta name=viewport content=\"width=device-width\"><title>" + html.EscapeString(title) + "</title><main><h1>" + html.EscapeString(title) + "</h1><p>" + html.EscapeString(message) + "</p><p>" + html.EscapeString(block.Reason) + "</p><p>Incident: <code>" + html.EscapeString(block.IncidentID) + "</code></p><p>" + html.EscapeString(support) + "</p></main>"
}

func normalizeSecurityIP(value string) string {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil { return "" }
	return ip.String()
}

func boundedPath(value string) string {
	if len(value) > 256 { return value[:256] }
	return value
}

func cleanSecurityText(value string, limit int) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune { if r < ' ' { return -1 }; return r }, value))
	if len(value) > limit { value = value[:limit] }
	return value
}

func randomIncidentID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil { return hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000"))) }
	return hex.EncodeToString(b[:])
}
