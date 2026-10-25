package httpsecurity

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestDecisionAdmissionNeverAllowsAnUnavailableOwner(t *testing.T) {
	attack := &AttackGuard{shards: make([]chan attackGuardRequest, attackGuardShardCount), done: make(chan struct{})}
	throttle := &ThrottleGuard{shards: make([]chan throttleRequest, throttleGuardShardCount), done: make(chan struct{})}
	for index := range attack.shards {
		attack.shards[index] = make(chan attackGuardRequest, 1)
		attack.shards[index] <- attackGuardRequest{}
		throttle.shards[index] = make(chan throttleRequest, 1)
		throttle.shards[index] <- throttleRequest{}
	}
	ip := "203.0.113.8"
	if _, err := attack.Decide(AttackRequest{IP: ip, Method: "GET"}, nil); err == nil {
		t.Fatal("a full attack queue returned an allow decision")
	}
	if _, err := throttle.Decide(ThrottleRequest{IP: ip}, nil); err == nil {
		t.Fatal("a full throttle queue returned an allow decision")
	}
	close(attack.done)
	close(throttle.done)
	if _, err := attack.Decide(AttackRequest{IP: ip}, nil); err == nil {
		t.Fatal("a stopped attack owner returned a decision")
	}
	if _, err := throttle.Decide(ThrottleRequest{IP: ip}, nil); err == nil {
		t.Fatal("a stopped throttle owner returned a decision")
	}
}

func TestDecisionCountsTheThresholdRequestAndCopiesEvidence(t *testing.T) {
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	request := AttackRequest{Domain: "example.org", IP: "203.0.113.8", Method: "POST", Path: "/", PolicyTrust: true, Now: time.Now()}
	for count := 1; count <= 40; count++ {
		decision, decisionErr := guard.Decide(request, nil)
		if decisionErr != nil || decision.Blocked != (count == 40) {
			t.Fatalf("request %d: decision=%+v err=%v", count, decision, decisionErr)
		}
		if count == 40 {
			decision.Block.ReasonLog[0].Description = "consumer mutation"
		}
	}
	block, blocked := guard.CheckSite(request.Domain, request.IP, request.Now)
	if !blocked || block.ReasonLog[0].Description == "consumer mutation" {
		t.Fatal("a response retained ownership of the guard's history", block)
	}
	severe, err := guard.Decide(AttackRequest{Domain: request.Domain, IP: "203.0.113.9", Category: "traversal", PolicyTrust: true, Now: request.Now}, nil)
	if err != nil || !severe.Blocked {
		t.Fatal("the current traversal request escaped enforcement", severe, err)
	}
}

func TestCanceledDecisionCannotHoldItsShardOrSpendRateBudget(t *testing.T) {
	guard, err := NewThrottleGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	canceled := make(chan struct{})
	close(canceled)
	if _, err := guard.Decide(ThrottleRequest{IP: "203.0.113.8"}, canceled); err == nil {
		t.Fatal("canceled work was accepted")
	}
	// A departed reply consumer must release the shard even after state changed.
	disconnected := make(chan struct{})
	guard.shards[throttleShardIndex("203.0.113.8")] <- throttleRequest{IP: "203.0.113.8", Reply: make(chan throttleResult), Done: disconnected}
	close(disconnected)
	finished := make(chan error, 1)
	go func() {
		_, decisionErr := guard.Decide(ThrottleRequest{IP: "203.0.113.8", Now: time.Now()}, nil)
		finished <- decisionErr
	}()
	select {
	case decisionErr := <-finished:
		if decisionErr != nil {
			t.Fatal(decisionErr)
		}
	case <-time.After(time.Second):
		t.Fatal("a departed consumer retained its shard")
	}
}

func TestRemoteThrottleSharesOneBudgetAndPersistence(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "throttleguard.json")
	owner, err := NewThrottleGuard(filename)
	if err != nil {
		t.Fatal(err)
	}
	remote := NewRemoteThrottleGuard(owner.Relay)
	started := time.Now().Add(-61 * time.Second)
	const ip = "203.0.113.8"
	for count := 0; count <= 600; count++ {
		if _, err := owner.Decide(ThrottleRequest{IP: ip, Now: started.Add(time.Duration(count) * time.Second / 10)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	activeAt := started.Add(61 * time.Second)
	for count := 0; count < 9; count++ {
		participant := owner
		if count%2 == 1 {
			participant = remote
		}
		decision, decisionErr := participant.Decide(ThrottleRequest{IP: ip, Now: activeAt}, nil)
		if decisionErr != nil || !decision.Active || decision.RateLimited != (count == 8) {
			t.Fatalf("shared request %d: %+v %v", count, decision, decisionErr)
		}
	}
	remote.Close()
	owner.Close()
	restored, err := NewThrottleGuard(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	blocks, err := restored.Snapshot(time.Now())
	if err != nil || len(blocks) != 1 {
		t.Fatal("rate state did not survive owner restart", blocks, err)
	}
	if err := NewRemoteThrottleGuard(restored.Relay).Remove(ip); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.Relay([]byte(`{"Operation":99}`)); err == nil {
		t.Fatal("unknown remote operation admitted")
	}
	if _, err := restored.Relay([]byte("broken")); err == nil {
		t.Fatal("malformed remote operation admitted")
	}
	if _, err := json.Marshal(throttleRequest{Reply: make(chan throttleResult), Done: make(chan struct{})}); err != nil {
		t.Fatal("local session capabilities entered remote encoding", err)
	}
}
