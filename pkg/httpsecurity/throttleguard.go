package httpsecurity

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
	throttleGuardShardCount       = 16
	throttleGuardQueueSize        = 256
	throttleSustainedMinimum      = time.Minute
	throttleSustainedMinimumCount = 600
	throttleContinuityGap         = 5 * time.Second
	throttleAllowedRPS            = 8
	throttleHistoryTTL            = 30 * 24 * time.Hour
)

type SecurityThrottle struct {
	IP          string    `json:"ip"`
	Reason      string    `json:"reason"`
	Description string    `json:"description"`
	LastEvent   time.Time `json:"last_event"`
	ExpiresAt   time.Time `json:"expires_at"`
	Episodes    int       `json:"episodes"`
	LimitRPS    int       `json:"limit_rps"`
}

type ThrottleDecision struct {
	Throttle    SecurityThrottle
	Active      bool
	RateLimited bool
	RetryAfter  time.Duration
}

type throttleObservation struct {
	Started time.Time
	Last    time.Time
	Count   int
}

type throttleRateWindow struct {
	Started time.Time
	Count   int
}

type throttleDiskState struct {
	Throttles []SecurityThrottle `json:"throttles"`
}

type throttleOperation uint8

const (
	throttleObserve throttleOperation = iota
	throttleSnapshot
	throttleHistory
	throttleRemove
)

type throttleRequest struct {
	Operation         throttleOperation
	IP                string
	Trusted           bool
	ExemptObservation bool
	Now               time.Time
	Reply             chan throttleResult
}

type throttleResult struct {
	Decision  ThrottleDecision
	Throttles []SecurityThrottle
	Changed   bool
	Err       error
}

type ThrottleGuard struct {
	shards   []chan throttleRequest
	save     chan struct{}
	shutdown chan struct{}
	done     chan struct{}
	path     string
}

// NewThrottleGuard keeps sustained-rate handling independent from attack
// blocking. Each shard owns its observations, throttle history, and rate state.
func NewThrottleGuard(path string) (*ThrottleGuard, error) {
	state, err := loadThrottleDiskState(path)
	if err != nil {
		return nil, err
	}
	guard := &ThrottleGuard{
		shards:   make([]chan throttleRequest, throttleGuardShardCount),
		save:     make(chan struct{}, 1),
		shutdown: make(chan struct{}),
		done:     make(chan struct{}),
		path:     path,
	}
	initial := make([][]SecurityThrottle, throttleGuardShardCount)
	for _, throttle := range state.Throttles {
		throttle.IP = normalizeThrottleIP(throttle.IP)
		if throttle.IP == "" {
			continue
		}
		index := throttleShardIndex(throttle.IP)
		initial[index] = append(initial[index], throttle)
	}
	for index := range guard.shards {
		requests := make(chan throttleRequest, throttleGuardQueueSize)
		guard.shards[index] = requests
		go runThrottleShard(requests, guard.shutdown, initial[index])
	}
	go guard.runPersistence()
	return guard, nil
}

func loadThrottleDiskState(path string) (throttleDiskState, error) {
	state := throttleDiskState{}
	if strings.TrimSpace(path) == "" {
		return state, nil
	}
	encoded, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return throttleDiskState{}, err
	}
	if err := json.Unmarshal(encoded, &state); err != nil {
		return throttleDiskState{}, err
	}
	return state, nil
}

func runThrottleShard(requests <-chan throttleRequest, shutdown <-chan struct{}, initial []SecurityThrottle) {
	observations := map[string]throttleObservation{}
	throttles := make(map[string]SecurityThrottle, len(initial))
	rates := map[string]throttleRateWindow{}
	for _, throttle := range initial {
		throttles[throttle.IP] = throttle
	}
	pruneTicker := time.NewTicker(time.Minute)
	defer pruneTicker.Stop()

	for {
		select {
		case <-shutdown:
			return
		case now := <-pruneTicker.C:
			pruneThrottleState(observations, throttles, rates, now.UTC())
		case request := <-requests:
			result := handleThrottleRequest(observations, throttles, rates, request)
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

func handleThrottleRequest(observations map[string]throttleObservation, throttles map[string]SecurityThrottle, rates map[string]throttleRateWindow, request throttleRequest) throttleResult {
	now := request.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ip := normalizeThrottleIP(request.IP)

	switch request.Operation {
	case throttleObserve:
		if request.Trusted || ip == "" {
			return throttleResult{}
		}
		if throttle, active := activeThrottle(throttles, ip, now); active {
			throttle.LastEvent = now
			throttles[ip] = throttle
			rate := rates[ip]
			if rate.Started.IsZero() || now.Sub(rate.Started) >= time.Second {
				rate = throttleRateWindow{Started: now}
			}
			rate.Count++
			rates[ip] = rate
			decision := ThrottleDecision{Throttle: throttle, Active: true}
			if rate.Count > throttleAllowedRPS {
				decision.RateLimited = true
				decision.RetryAfter = time.Second
			}
			return throttleResult{Decision: decision}
		}
		if request.ExemptObservation {
			return throttleResult{}
		}

		observation := observations[ip]
		if observation.Started.IsZero() || now.Sub(observation.Last) > throttleContinuityGap {
			observation = throttleObservation{Started: now}
		}
		observation.Last = now
		observation.Count++
		observations[ip] = observation

		if now.Sub(observation.Started) < throttleSustainedMinimum || observation.Count < throttleSustainedMinimumCount {
			return throttleResult{}
		}

		throttle := beginThrottle(throttles[ip], ip, now)
		throttles[ip] = throttle
		delete(observations, ip)
		rates[ip] = throttleRateWindow{Started: now, Count: 1}
		return throttleResult{
			Decision: ThrottleDecision{Throttle: throttle, Active: true},
			Changed:  true,
		}

	case throttleSnapshot:
		snapshot := make([]SecurityThrottle, 0, len(throttles))
		for _, throttle := range throttles {
			if now.Before(throttle.ExpiresAt) {
				snapshot = append(snapshot, throttle)
			}
		}
		return throttleResult{Throttles: snapshot}

	case throttleHistory:
		history := make([]SecurityThrottle, 0, len(throttles))
		for _, throttle := range throttles {
			if now.Sub(throttle.ExpiresAt) <= throttleHistoryTTL {
				history = append(history, throttle)
			}
		}
		return throttleResult{Throttles: history}

	case throttleRemove:
		_, found := throttles[ip]
		delete(throttles, ip)
		delete(observations, ip)
		delete(rates, ip)
		return throttleResult{Changed: found}
	}

	return throttleResult{Err: errors.New("unknown throttle operation")}
}

func activeThrottle(throttles map[string]SecurityThrottle, ip string, now time.Time) (SecurityThrottle, bool) {
	throttle, found := throttles[ip]
	if !found || !now.Before(throttle.ExpiresAt) {
		return SecurityThrottle{}, false
	}
	return throttle, true
}

func beginThrottle(previous SecurityThrottle, ip string, now time.Time) SecurityThrottle {
	episodes := previous.Episodes + 1
	if episodes < 1 {
		episodes = 1
	}
	return SecurityThrottle{
		IP:          ip,
		Reason:      "sustained-high-rate",
		Description: "high request rate continued for at least one minute",
		LastEvent:   now,
		ExpiresAt:   now.Add(throttleDuration(episodes)),
		Episodes:    episodes,
		LimitRPS:    throttleAllowedRPS,
	}
}

func throttleDuration(episodes int) time.Duration {
	durations := [...]time.Duration{
		time.Hour,
		3 * time.Hour,
		6 * time.Hour,
		12 * time.Hour,
		24 * time.Hour,
		48 * time.Hour,
		96 * time.Hour,
		7 * 24 * time.Hour,
	}
	if episodes <= 0 {
		return durations[0]
	}
	index := episodes - 1
	if index >= len(durations) {
		index = len(durations) - 1
	}
	return durations[index]
}

func pruneThrottleState(observations map[string]throttleObservation, throttles map[string]SecurityThrottle, rates map[string]throttleRateWindow, now time.Time) {
	for ip, observation := range observations {
		if now.Sub(observation.Last) > throttleContinuityGap {
			delete(observations, ip)
		}
	}
	for ip, throttle := range throttles {
		if now.Sub(throttle.ExpiresAt) > throttleHistoryTTL {
			delete(throttles, ip)
			delete(rates, ip)
		}
	}
	for ip, rate := range rates {
		if now.Sub(rate.Started) > time.Minute {
			delete(rates, ip)
		}
	}
}

func (guard *ThrottleGuard) ObserveFast(ip string, trusted bool, now time.Time) ThrottleDecision {
	return guard.observeFast(ip, trusted, false, now)
}

// ObserveFastExemptObservation keeps an already active throttle enforceable while
// excluding this request from the sustained-rate history that can create one.
func (guard *ThrottleGuard) ObserveFastExemptObservation(ip string, trusted bool, now time.Time) ThrottleDecision {
	return guard.observeFast(ip, trusted, true, now)
}

func (guard *ThrottleGuard) observeFast(ip string, trusted, exemptObservation bool, now time.Time) ThrottleDecision {
	result, ok := guard.exchangeFast(throttleRequest{Operation: throttleObserve, IP: ip, Trusted: trusted, ExemptObservation: exemptObservation, Now: now})
	if !ok {
		return ThrottleDecision{}
	}
	if result.Changed {
		guard.signalSave()
	}
	return result.Decision
}

func (guard *ThrottleGuard) Snapshot(now time.Time) ([]SecurityThrottle, error) {
	return guard.collect(now, throttleSnapshot)
}

func (guard *ThrottleGuard) history(now time.Time) ([]SecurityThrottle, error) {
	return guard.collect(now, throttleHistory)
}

func (guard *ThrottleGuard) collect(now time.Time, operation throttleOperation) ([]SecurityThrottle, error) {
	throttles := []SecurityThrottle{}
	for index := range guard.shards {
		result, ok := guard.exchangeShardAdmin(index, throttleRequest{Operation: operation, Now: now})
		if !ok {
			return nil, errors.New("throttle guard is busy")
		}
		if result.Err != nil {
			return nil, result.Err
		}
		throttles = append(throttles, result.Throttles...)
	}
	sort.Slice(throttles, func(i, j int) bool {
		if throttles[i].LastEvent.Equal(throttles[j].LastEvent) {
			return throttles[i].IP < throttles[j].IP
		}
		return throttles[i].LastEvent.After(throttles[j].LastEvent)
	})
	return throttles, nil
}

func (guard *ThrottleGuard) Remove(ip string) error {
	result, ok := guard.exchangeAdmin(throttleRequest{Operation: throttleRemove, IP: ip, Now: time.Now().UTC()})
	if !ok {
		return errors.New("throttle guard is busy")
	}
	if result.Changed {
		guard.signalSave()
	}
	return result.Err
}

func (guard *ThrottleGuard) exchangeFast(request throttleRequest) (throttleResult, bool) {
	ip := normalizeThrottleIP(request.IP)
	if ip == "" {
		return throttleResult{}, true
	}
	request.IP = ip
	index := throttleShardIndex(ip)
	reply := make(chan throttleResult, 1)
	request.Reply = reply
	select {
	case <-guard.done:
		return throttleResult{}, false
	case guard.shards[index] <- request:
	default:
		return throttleResult{}, false
	}
	select {
	case <-guard.done:
		return throttleResult{}, false
	case result := <-reply:
		return result, true
	}
}

func (guard *ThrottleGuard) exchangeAdmin(request throttleRequest) (throttleResult, bool) {
	ip := normalizeThrottleIP(request.IP)
	if ip == "" {
		return throttleResult{Err: errors.New("invalid IP address")}, true
	}
	request.IP = ip
	return guard.exchangeShardAdmin(throttleShardIndex(ip), request)
}

func (guard *ThrottleGuard) exchangeShardAdmin(index int, request throttleRequest) (throttleResult, bool) {
	reply := make(chan throttleResult, 1)
	request.Reply = reply
	select {
	case <-guard.done:
		return throttleResult{}, false
	case guard.shards[index] <- request:
	default:
		return throttleResult{}, false
	}
	select {
	case <-guard.done:
		return throttleResult{}, false
	case result := <-reply:
		return result, true
	}
}

func throttleShardIndex(ip string) int {
	var hash uint32 = 2166136261
	for index := 0; index < len(ip); index++ {
		hash ^= uint32(ip[index])
		hash *= 16777619
	}
	return int(hash % throttleGuardShardCount)
}

func normalizeThrottleIP(value string) string {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return ""
	}
	return ip.String()
}

func (guard *ThrottleGuard) signalSave() {
	select {
	case guard.save <- struct{}{}:
	default:
	}
}

func (guard *ThrottleGuard) runPersistence() {
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

func (guard *ThrottleGuard) saveSnapshot() error {
	if strings.TrimSpace(guard.path) == "" {
		return nil
	}
	throttles, err := guard.history(time.Now().UTC())
	if err != nil {
		return err
	}
	state := throttleDiskState{Throttles: throttles}
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(guard.path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(guard.path), ".throttle-*")
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

func (guard *ThrottleGuard) Close() {
	_ = guard.saveSnapshot()
	close(guard.shutdown)
	<-guard.done
}
