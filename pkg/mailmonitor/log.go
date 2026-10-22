package mailmonitor

import (
	"fmt"
	"strings"
	"time"
)

type LogMessage struct {
	Key  string
	Text string
}

type logWindow struct {
	First   time.Time
	Last    time.Time
	Count   int
	Pending int
	Text    string
}

// One goroutine owns aggregation. Database audit work never waits for a slow
// process-log sink, and attacker-controlled recipients cannot multiply keys.
func StartLogWriter(stop <-chan struct{}, write func(string)) chan<- LogMessage {
	messages := make(chan LogMessage, 256)
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		runLogWriter(stop, messages, ticker.C, time.Now, write)
	}()
	return messages
}

// Clock input belongs to this worker, allowing deterministic session and flush boundaries.
func runLogWriter(stop <-chan struct{}, messages <-chan LogMessage, ticks <-chan time.Time, now func() time.Time, write func(string)) {
	windows := map[string]logWindow{}
	flush := func(timestamp time.Time) {
		for key, window := range windows {
			if window.Pending > 0 {
				write(fmt.Sprintf("MAIL series count=%d additional=%d first=%s last=%s %s", window.Count, window.Pending, window.First.Local().Format("02 Jan 2006 15:04:05 -07:00"), window.Last.Local().Format("02 Jan 2006 15:04:05 -07:00"), window.Text))
				window.Pending = 0
				windows[key] = window
			}
			if timestamp.Sub(window.First) >= 10*time.Minute {
				delete(windows, key)
			}
		}
	}
	for {
		select {
		case <-stop:
			flush(now())
			return
		case timestamp, open := <-ticks:
			if !open {
				ticks = nil
				continue
			}
			flush(timestamp)
		case message, open := <-messages:
			if !open {
				flush(now())
				return
			}
			timestamp := now()
			key := message.Key
			if _, found := windows[key]; !found && len(windows) >= 4096 {
				key = "overflow"
			}
			window := windows[key]
			if window.First.IsZero() {
				window.First = timestamp
			}
			window.Last = timestamp
			window.Count++
			window.Text = message.Text
			if window.Count < 5 {
				write(message.Text)
			} else {
				window.Pending++
			}
			windows[key] = window
		}
	}
}

func LogKey(origin, ip, kind, result string) string {
	return strings.Join([]string{origin, ip, kind, result}, "\x1f")
}
