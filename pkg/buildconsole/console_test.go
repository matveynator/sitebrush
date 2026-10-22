package buildconsole

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestConfirmationRequiresCompleteInput(t *testing.T) {
	for _, scenario := range []struct {
		input    string
		fallback bool
		want     bool
		wantErr  bool
	}{
		{"", true, false, true}, {"yes", false, false, true},
		{"\n", false, false, false}, {"\n", true, true, false},
		{"maybe\nyes\n", false, true, false}, {"NO\n", true, false, false},
	} {
		console, _ := New(io.Discard, "never")
		answer, err := console.Confirm(bufio.NewReader(strings.NewReader(scenario.input)), "Continue?", scenario.fallback)
		if answer != scenario.want || (err != nil) != scenario.wantErr {
			t.Errorf("input=%q answer=%t err=%v", scenario.input, answer, err)
		}
	}
}

func TestPoliciesAndColorOutput(t *testing.T) {
	if _, err := New(io.Discard, "invalid"); err == nil {
		t.Fatal("invalid color policy accepted")
	}
	if _, err := Interactive("invalid", false, strings.NewReader(""), io.Discard); err == nil {
		t.Fatal("invalid interactive policy accepted")
	}
	for _, policy := range []string{"auto", "never", "always"} {
		interactive, err := Interactive(policy, false, strings.NewReader(""), io.Discard)
		if err != nil || interactive != (policy == "always") {
			t.Fatalf("nonterminal policy=%s interactive=%t err=%v", policy, interactive, err)
		}
		var output bytes.Buffer
		console, _ := New(&output, policy)
		console.Heading("Build")
		console.OK("Ready")
		answer, err := console.Line(bufio.NewReader(strings.NewReader("\n")), "Your choice", "Enter")
		if err != nil || answer != "Enter" {
			t.Fatalf("default input changed while styling it: %q, %v", answer, err)
		}
		if strings.Contains(output.String(), "\x1b[") != (policy == "always") {
			t.Errorf("unexpected colors for %s: %q", policy, output.String())
		}
		if policy == "always" && !strings.Contains(output.String(), "[\x1b[1mEnter\x1b[0m]") {
			t.Fatal("the default choice is not bold")
		}
	}
}

func TestStepReportsFailureWithoutClaimingSuccess(t *testing.T) {
	var output bytes.Buffer
	console, _ := New(&output, "never")
	failure := errors.New("compiler failed")
	err := console.Step(1, 2, "Compile", func() error { return failure })
	if !errors.Is(err, failure) || !strings.Contains(output.String(), "[FAILED]") || strings.Contains(output.String(), "[OK]") {
		t.Fatalf("failure was hidden: %v, %s", err, output.String())
	}
}
