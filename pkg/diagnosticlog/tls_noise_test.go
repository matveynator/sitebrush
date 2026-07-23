package diagnosticlog

import (
	"testing"
	"time"
)

func TestParseTLSHandshakeNoiseGroupsKnownErrorsBySubnet(t *testing.T) {
	testCases := []struct {
		message    string
		noiseClass string
		source     string
	}{
		{
			message:    "http: TLS handshake error from 127.0.0.1:53497: EOF",
			noiseClass: "connection_closed",
			source:     "loopback",
		},
		{
			message:    "http: TLS handshake error from 203.0.113.87:44321: unexpected EOF",
			noiseClass: "connection_closed",
			source:     "203.0.113.0/24",
		},
		{
			message:    "http: TLS handshake error from [2001:db8:1234:5678::12]:44321: remote error: tls: bad certificate",
			noiseClass: "certificate_rejected",
			source:     "2001:db8:1234:5678::/64",
		},
	}
	for _, testCase := range testCases {
		noise, ok := ParseTLSHandshakeNoise(testCase.message)
		if !ok {
			t.Fatalf("message was not classified: %s", testCase.message)
		}
		if noise.Class != testCase.noiseClass || noise.Source != testCase.source || noise.Message != testCase.message {
			t.Fatalf("noise = %+v, want class=%s source=%s", noise, testCase.noiseClass, testCase.source)
		}
	}
}

func TestParseTLSHandshakeNoiseLeavesUnknownErrorsImmediate(t *testing.T) {
	messages := []string{
		"http: TLS handshake error from 127.0.0.1:53497: tls: internal error",
		"http: panic serving 127.0.0.1:53497: runtime error",
		"http: TLS handshake error from malformed-address: EOF",
	}
	for _, message := range messages {
		if noise, ok := ParseTLSHandshakeNoise(message); ok {
			t.Fatalf("unexpected classification for %q: %+v", message, noise)
		}
	}
}

func TestTLSHandshakeNoiseIntervalUsesProgressiveBackoff(t *testing.T) {
	expectedIntervals := []time.Duration{
		5 * time.Minute,
		30 * time.Minute,
		time.Hour,
		6 * time.Hour,
		24 * time.Hour,
		24 * time.Hour,
	}
	for summaryCount, expectedInterval := range expectedIntervals {
		if interval := TLSHandshakeNoiseInterval(summaryCount); interval != expectedInterval {
			t.Fatalf("interval %d = %s, want %s", summaryCount, interval, expectedInterval)
		}
	}
}
