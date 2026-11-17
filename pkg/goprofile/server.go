// Package goprofile owns an optional process-local diagnostics listener.
package goprofile

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"
	"time"
)

// Session keeps diagnostic shutdown independent of the public HTTP server.
type Session struct {
	address string
	closes  chan struct{}
	done    chan struct{}
	tasks   chan task
}

// Start binds only loopback. Each process gets its own port, including during updates.
func Start(role, version, logOutput string, stop <-chan struct{}) (*Session, error) {
	if role != "public" && role != "application" {
		return nil, errors.New("unknown profiler process role")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("open profiler listener: %w", err)
	}
	session := &Session{address: listener.Addr().String(), closes: make(chan struct{}), done: make(chan struct{}), tasks: make(chan task, 16)}
	server := &http.Server{Handler: diagnosticHandler(role, version), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16 << 10}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	go session.run(role, version, server, served, stop)
	filename := fmt.Sprintf("cpu-%s-%d.pprof", role, os.Getpid())
	log.Printf("PROFILER role=%s pid=%d version=%q url=http://%s/debug/pprof/ journal=%q", role, os.Getpid(), version, session.address, logOutput)
	log.Printf("PROFILER role=%s capture: curl --fail --max-time 40 -o ./%s 'http://%s/debug/pprof/profile?seconds=30'", role, filename, session.address)
	log.Printf("PROFILER role=%s output=./%s in the curl client's current directory; the profile is saved by the client", role, filename)
	return session, nil
}

func (session *Session) Address() string { return session.address }

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

// Use an explicit handler so diagnostic routes never enter the website router.
func diagnosticHandler(role, version string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "use GET", http.StatusMethodNotAllowed)
			return
		}
		// Reject browser requests from other origins, including DNS rebinding names.
		host, _, hostErr := net.SplitHostPort(request.Host)
		if hostErr != nil || host != "127.0.0.1" || request.Header.Get("Origin") != "" && request.Header.Get("Origin") != "http://"+request.Host {
			http.Error(writer, "use the local profiler address", http.StatusForbidden)
			return
		}
		if seconds := request.URL.Query().Get("seconds"); seconds != "" {
			duration, parseErr := strconv.Atoi(seconds)
			if parseErr != nil || duration < 1 || duration > 120 {
				http.Error(writer, "seconds must be between 1 and 120", http.StatusBadRequest)
				return
			}
		}
		switch request.URL.Path {
		case "/debug/pprof/":
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintf(writer, "SiteBrush profiler role=%s pid=%d version=%s\nCPU: /debug/pprof/profile?seconds=30\nMemory: /debug/pprof/heap\nAllocations: /debug/pprof/allocs\nGoroutines: /debug/pprof/goroutine?debug=2\nSave the response using curl -o ./profile.pprof; files are written in the curl client's current directory.\n", role, os.Getpid(), version)
		case "/debug/pprof/profile":
			pprof.Profile(writer, request)
		case "/debug/pprof/heap":
			pprof.Handler("heap").ServeHTTP(writer, request)
		case "/debug/pprof/allocs":
			pprof.Handler("allocs").ServeHTTP(writer, request)
		case "/debug/pprof/goroutine":
			pprof.Handler("goroutine").ServeHTTP(writer, request)
		default:
			http.NotFound(writer, request)
		}
	})
}
