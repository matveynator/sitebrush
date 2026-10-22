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
		windows := map[string]logWindow{}
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		flush := func(now time.Time) {
			for key, window := range windows {
				if window.Pending > 0 {
					write(fmt.Sprintf("MAIL series count=%d additional=%d first=%s last=%s %s", window.Count, window.Pending, window.First.Local().Format("02 Jan 2006 15:04:05 -07:00"), window.Last.Local().Format("02 Jan 2006 15:04:05 -07:00"), window.Text))
					window.Pending = 0
					windows[key] = window
				}
				if now.Sub(window.First) >= 10*time.Minute {
					delete(windows, key)
				}
			}
		}
		for {
			select {
			case <-stop:
				flush(time.Now())
				return
			case now := <-ticker.C:
				flush(now)
			case message := <-messages:
				now := time.Now()
				key := message.Key
				if _, found := windows[key]; !found && len(windows) >= 4096 {
					key = "overflow"
				}
				window := windows[key]
				if window.First.IsZero() {
					window.First = now
				}
				window.Last = now
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
	}()
	return messages
}

func LogKey(origin, ip, kind, result string) string {
	return strings.Join([]string{origin, ip, kind, result}, "\x1f")
}
