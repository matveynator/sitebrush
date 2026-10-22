package mailmonitor

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMailLogWriterFlushesSeriesAndExpiresWindows(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	messages := make(chan LogMessage)
	ticks := make(chan time.Time)
	output := make(chan string, 16)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runLogWriter(nil, messages, ticks, func() time.Time { return now }, func(line string) { output <- line })
	}()
	key := LogKey("desktop", "192.0.2.1", "recovery", "failed")
	for index := range 7 {
		messages <- LogMessage{Key: key, Text: fmt.Sprintf("attempt %d", index)}
	}
	ticks <- now.Add(11 * time.Minute)
	messages <- LogMessage{Key: key, Text: "fresh window"}
	close(messages)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("log consumer did not close")
	}
	close(output)
	var lines []string
	for line := range output {
		lines = append(lines, line)
	}
	if len(lines) != 6 || !strings.Contains(lines[4], "count=7 additional=3") || lines[5] != "fresh window" {
		t.Fatalf("wrong bounded series: %v", lines)
	}
	if key == LogKey("desktop", "192.0.2.1", "registration", "failed") {
		t.Fatal("unrelated activities shared a log key")
	}
}

func TestMailLogWriterBoundsDistinctSourcesAndFlushesOnStop(t *testing.T) {
	messages := make(chan LogMessage)
	stop := make(chan struct{})
	output := make(chan string, 4200)
	finished := make(chan struct{})
	now := time.Now()
	go func() {
		defer close(finished)
		runLogWriter(stop, messages, nil, func() time.Time { return now }, func(line string) { output <- line })
	}()
	for index := range 4101 {
		messages <- LogMessage{Key: fmt.Sprint(index), Text: "bounded"}
	}
	close(stop)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not flush logs")
	}
	close(output)
	count, summaries := 0, 0
	for line := range output {
		count++
		if strings.Contains(line, "series count=5 additional=1") {
			summaries++
		}
	}
	if count != 4101 || summaries != 1 {
		t.Fatalf("unbounded distinct-source logs: count=%d summaries=%d", count, summaries)
	}
}

func TestStartedMailLogWriterUsesConsumerChannel(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	output := make(chan string, 1)
	messages := StartLogWriter(stop, func(line string) { output <- line })
	messages <- LogMessage{Key: "one", Text: "delivered"}
	select {
	case line := <-output:
		if line != "delivered" {
			t.Fatal(line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("log was not delivered")
	}
	close(messages)
}
