// Package goprofile owns on-demand load recordings viewed by the server owner.
package goprofile

import (
	"errors"
	"log"
	"os"
)

// Session communicates with one participant owning captures and their temporary files.
type Session struct {
	closes chan struct{}
	done   chan struct{}
	tasks  chan task
}

func Start(role, version, logOutput string, stop <-chan struct{}) (*Session, error) {
	if role != "public" && role != "application" {
		return nil, errors.New("unknown profiler process role")
	}
	session := &Session{closes: make(chan struct{}), done: make(chan struct{}), tasks: make(chan task, 16)}
	go session.run(role, version, stop)
	log.Printf("PROFILER role=%s pid=%d version=%q journal=%q view=/?expenses; recordings start only from the owner load-test dialog; temporary data is removed when viewing ends", role, os.Getpid(), version, logOutput)
	return session, nil
}
func (session *Session) Close() {
	if session == nil {
		return
	}
	select {
	case session.closes <- struct{}{}:
	case <-session.done:
	}
	<-session.done
}
