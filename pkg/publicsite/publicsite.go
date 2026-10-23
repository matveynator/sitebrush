// Package publicsite keeps the public HTTP boundary alive independently of writers.
package publicsite

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

const ProxyHeader = "X-SiteBrush-Public-Contact"
const RemoteHeader = "X-SiteBrush-Public-Remote"
const SchemeHeader = "X-SiteBrush-Public-Scheme"
const MetadataHeader = "X-SiteBrush-Public-Metadata"
const InternalHeader = "X-SiteBrush-Public-Internal"

type requestMetadata struct {
	Headers      http.Header
	Certificates [][]byte
}

func forwardingHeaderNames() [5]string {
	return [5]string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"}
}

func proxyMetadata(r *http.Request) string {
	metadata := requestMetadata{Headers: make(http.Header)}
	for _, name := range forwardingHeaderNames() {
		for _, header := range r.Header.Values(name) {
			metadata.Headers.Add(name, header)
		}
	}
	if r.TLS != nil {
		for _, certificate := range r.TLS.PeerCertificates {
			metadata.Certificates = append(metadata.Certificates, certificate.Raw)
		}
	}
	encoded, _ := json.Marshal(metadata)
	return base64.StdEncoding.EncodeToString(encoded)
}

type State struct {
	Address string
	Paused  bool
}
type stateTask struct {
	next  *State
	reply chan State
}
type Front struct {
	HTTPPort     int
	HTTPSPort    int
	Security     func([]byte) ([]byte, error)
	Certificate  func(string) error
	observations chan string
	events       chan publicObservation
	losses       chan string
	tasks        chan stateTask
	stop         <-chan struct{}
	secret       string
	public       func(http.ResponseWriter, *http.Request, bool) bool
}

func NewFront(secret string, public func(http.ResponseWriter, *http.Request, bool) bool, stop <-chan struct{}) *Front {
	front := &Front{events: make(chan publicObservation, 1024), losses: make(chan string, 64), observations: make(chan string, 64), tasks: make(chan stateTask, 128), stop: stop, secret: secret, public: public}
	go func() {
		state := State{Paused: true}
		for {
			select {
			case <-stop:
				return
			case task := <-front.tasks:
				if task.next != nil {
					state = *task.next
				}
				task.reply <- state
			}
		}
	}()
	go front.observeCertificates()
	go front.deliverObservations()
	return front
}
func (front *Front) exchange(next *State) (State, error) {
	reply := make(chan State, 1)
	select {
	case front.tasks <- stateTask{next: next, reply: reply}:
	case <-front.stop:
		return State{}, errors.New("public server stopped")
	}
	select {
	case state := <-reply:
		return state, nil
	case <-front.stop:
		return State{}, errors.New("public server stopped")
	}
}
func (front *Front) Set(address string, paused bool) error {
	_, err := front.exchange(&State{Address: address, Paused: paused})
	return err
}
func (front *Front) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	state, err := front.exchange(nil)
	if err != nil {
		http.Error(w, "server unavailable", http.StatusServiceUnavailable)
		return
	}
	if front.public(w, r, state.Paused) {
		return
	}
	// Only the read-only status boundary remains connected during replacement.
	if state.Address == "" || state.Paused {
		w.Header().Set("Retry-After", "2")
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	target := &url.URL{Scheme: "http", Host: state.Address}
	proxy := httputil.ReverseProxy{Rewrite: func(request *httputil.ProxyRequest) {
		request.SetURL(target)
		request.Out.Host = request.In.Host
		request.Out.Header.Del("Forwarded")
		request.Out.Header.Del("X-Forwarded-For")
		request.Out.Header.Del("X-Forwarded-Host")
		request.Out.Header.Del("X-Forwarded-Proto")
		request.Out.Header.Del(InternalHeader)
		request.Out.Header.Set(MetadataHeader, proxyMetadata(request.In))
		request.Out.Header.Set(ProxyHeader, front.secret)
		request.Out.Header.Set(RemoteHeader, request.In.RemoteAddr)
		scheme := "http"
		if request.In.TLS != nil {
			scheme = "https"
		}
		request.Out.Header.Set(SchemeHeader, scheme)
	}, ErrorHandler: func(writer http.ResponseWriter, request *http.Request, proxyError error) {
		if !front.public(writer, request, true) {
			http.Error(writer, "temporarily unavailable", http.StatusServiceUnavailable)
		}
	}}
	proxy.ServeHTTP(w, r)
}

// RestoreRequest accepts forwarding claims only from this launcher's private contact.
func RestoreRequest(next http.Handler, secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if secret == "" || err != nil || !net.ParseIP(host).IsLoopback() || subtle.ConstantTimeCompare([]byte(r.Header.Get(ProxyHeader)), []byte(secret)) != 1 {
			http.NotFound(w, r)
			return
		}
		r.RemoteAddr = r.Header.Get(RemoteHeader)
		r.URL.Scheme = r.Header.Get(SchemeHeader)
		for _, name := range forwardingHeaderNames() {
			r.Header.Del(name)
		}
		if r.URL.Scheme == "https" {
			r.TLS = &tls.ConnectionState{}
		}
		if encoded := r.Header.Get(MetadataHeader); encoded != "" {
			decoded, decodeErr := base64.StdEncoding.DecodeString(encoded)
			var metadata requestMetadata
			if decodeErr != nil || json.Unmarshal(decoded, &metadata) != nil {
				http.Error(w, "invalid public request", http.StatusBadRequest)
				return
			}
			for _, name := range forwardingHeaderNames() {
				for _, header := range metadata.Headers.Values(name) {
					r.Header.Add(name, header)
				}
			}
			if r.TLS != nil {
				for _, encodedCertificate := range metadata.Certificates {
					certificate, parseErr := x509.ParseCertificate(encodedCertificate)
					if parseErr != nil {
						http.Error(w, "invalid client certificate", http.StatusBadRequest)
						return
					}
					r.TLS.PeerCertificates = append(r.TLS.PeerCertificates, certificate)
				}
			}
		}
		r.Header.Del(MetadataHeader)
		r.Header.Del(ProxyHeader)
		r.Header.Del(RemoteHeader)
		r.Header.Del(SchemeHeader)
		next.ServeHTTP(w, r)
	})
}

func (front *Front) ObserveTLS(domain string) {
	select {
	case front.observations <- domain:
	default:
	}
}
func (front *Front) observeCertificates() {
	client := &http.Client{Timeout: 2 * time.Second}
	for {
		select {
		case <-front.stop:
			return
		case domain := <-front.observations:
			state, err := front.exchange(nil)
			if err != nil || state.Paused || state.Address == "" {
				continue
			}
			address := url.URL{Scheme: "http", Host: state.Address, Path: "/.well-known/sitebrush-public-tls", RawQuery: url.Values{"domain": {domain}}.Encode()}
			request, err := http.NewRequest(http.MethodGet, address.String(), nil)
			if err != nil {
				continue
			}
			request.Header.Set(InternalHeader, front.secret)
			request.Header.Set(ProxyHeader, front.secret)
			request.Header.Set(RemoteHeader, "127.0.0.1:0")
			request.Header.Set(SchemeHeader, "http")
			response, err := client.Do(request)
			if err == nil {
				response.Body.Close()
			}
		}
	}
}
