// Package publicsite keeps the public HTTP boundary alive independently of writers.
package publicsite

import (
	"context"
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

	"github.com/matveynator/sitebrush/v2/pkg/requesttiming"
)

const ProxyHeader = "X-SiteBrush-Public-Contact"
const RemoteHeader = "X-SiteBrush-Public-Remote"
const SchemeHeader = "X-SiteBrush-Public-Scheme"
const MetadataHeader = "X-SiteBrush-Public-Metadata"
const InternalHeader = "X-SiteBrush-Public-Internal"

type requestMetadata struct {
	Headers         http.Header
	Certificates    [][]byte
	SecurityChecked bool `json:",omitempty"`
}

type securityCheckedKey struct{}

// Only a validated private ingress restores this claim. Public headers never
// grant the right to skip an installation's security owners.
func SecurityChecked(request *http.Request) bool {
	checked, _ := request.Context().Value(securityCheckedKey{}).(bool)
	return checked
}

func CheckedRequest(request *http.Request) *http.Request {
	return request.WithContext(context.WithValue(request.Context(), securityCheckedKey{}, true))
}

func forwardingHeaderNames() [5]string {
	return [5]string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"}
}

func proxyMetadata(r *http.Request) string {
	metadata := requestMetadata{SecurityChecked: SecurityChecked(r)}
	for _, name := range forwardingHeaderNames() {
		for _, header := range r.Header.Values(name) {
			if metadata.Headers == nil {
				metadata.Headers = make(http.Header)
			}
			metadata.Headers.Add(name, header)
		}
	}
	if r.TLS != nil {
		for _, certificate := range r.TLS.PeerCertificates {
			metadata.Certificates = append(metadata.Certificates, certificate.Raw)
		}
	}
	if len(metadata.Headers) == 0 && len(metadata.Certificates) == 0 {
		if metadata.SecurityChecked {
			return "eyJTZWN1cml0eUNoZWNrZWQiOnRydWV9"
		}
		return ""
	}
	encoded, _ := json.Marshal(metadata)
	return base64.StdEncoding.EncodeToString(encoded)
}

type Change struct {
	Action  string
	Domain  string
	Address string
}

type State struct {
	PolicyReady bool
	Address     string
	Paused      bool
	Draining    bool
}
type stateTask struct {
	next  *State
	reply chan State
}
type Front struct {
	Guest        func(http.ResponseWriter, *http.Request) bool
	HTTPPort     int
	HTTPSPort    int
	Security     func([]byte) ([]byte, error)
	Throttle     func([]byte) ([]byte, error)
	Policy       func([]byte) ([]byte, error)
	ResetPolicy  func() error
	Certificate  func(string) error
	observations chan string
	pulls        chan observationPull
	events       chan publicObservation
	losses       chan string
	tasks        chan stateTask
	stop         <-chan struct{}
	secret       string
	public       func(http.ResponseWriter, *http.Request, bool) bool
	handler      http.Handler
	proxy        *httputil.ReverseProxy
}

func NewFront(secret string, public func(http.ResponseWriter, *http.Request, bool) bool, stop <-chan struct{}, admission ...func(http.Handler) http.Handler) *Front {
	front := &Front{pulls: make(chan observationPull, 1), events: make(chan publicObservation, 1024), losses: make(chan string, 64), observations: make(chan string, 64), tasks: make(chan stateTask, 128), stop: stop, secret: secret, public: public}
	front.proxy = newProxy(front)
	front.handler = http.HandlerFunc(front.serveHTTP)
	if len(admission) > 0 {
		front.handler = admission[0](front.handler)
	}
	go func() {
		state := State{Paused: true}
		for {
			select {
			case <-stop:
				return
			case task := <-front.tasks:
				if task.next != nil {
					previousAddress := state.Address
					state = *task.next
					if state.Paused && state.Address == "" {
						state.Address = previousAddress
					}
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
func (front *Front) Current() (State, error) { return front.exchange(nil) }
func (front *Front) PolicyEnabled() error {
	state, err := front.exchange(nil)
	if err != nil {
		return err
	}
	state.PolicyReady = true
	_, err = front.exchange(&state)
	return err
}
func (front *Front) Set(address string, paused bool) error {
	if !paused && front.ResetPolicy != nil {
		if err := front.ResetPolicy(); err != nil {
			return err
		}
	}
	_, err := front.exchange(&State{Address: address, Paused: paused, Draining: paused && address == ""})
	return err
}
func (front *Front) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	front.handler.ServeHTTP(w, r)
}

func (front *Front) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if front.Guest != nil && front.Guest(w, r) {
		return
	}
	started := requesttiming.Start(r)
	state, err := front.exchange(nil)
	requesttiming.Finish(r, requesttiming.FrontState, started)
	if err != nil {
		http.Error(w, "server unavailable", http.StatusServiceUnavailable)
		return
	}
	if front.public(w, r, state.Paused) {
		return
	}
	// While accepted writes finish, reads may still need their current policy
	// owner. After its snapshot is durable, replacement needs no database contact.
	readDuringDrain := state.Draining && (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.RawQuery == "" && r.Header.Get("Upgrade") == ""
	if state.Address == "" || state.Paused && !readDuringDrain {
		w.Header().Set("Retry-After", "2")
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	target := &url.URL{Scheme: "http", Host: state.Address}
	r = r.WithContext(context.WithValue(r.Context(), proxyTargetKey{}, target))
	started = requesttiming.Start(r)
	front.proxy.ServeHTTP(w, r)
	requesttiming.Finish(r, requesttiming.ProxyBackend, started)
}

type proxyTargetKey struct{}

// net/http owns transport synchronization. The selected address still comes
// exclusively from the Front.State owner and remains local to this request.
func newProxy(front *Front) *httputil.ReverseProxy {
	transport := &http.Transport{
		DialContext:  (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns: 1024, MaxIdleConnsPerHost: 1024,
		IdleConnTimeout: 90 * time.Second,
	}
	return &httputil.ReverseProxy{Transport: transport, Rewrite: func(request *httputil.ProxyRequest) {
		target := request.In.Context().Value(proxyTargetKey{}).(*url.URL)
		request.SetURL(target)
		request.Out.Host = request.In.Host
		request.Out.Header.Del("Forwarded")
		request.Out.Header.Del("X-Forwarded-For")
		request.Out.Header.Del("X-Forwarded-Host")
		request.Out.Header.Del("X-Forwarded-Proto")
		request.Out.Header.Del(InternalHeader)
		started := requesttiming.Start(request.In)
		metadata := proxyMetadata(request.In)
		requesttiming.Finish(request.In, requesttiming.ProxyMetadata, started)
		if metadata == "" {
			request.Out.Header.Del(MetadataHeader)
		} else {
			request.Out.Header.Set(MetadataHeader, metadata)
		}
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
			if metadata.SecurityChecked {
				r = CheckedRequest(r)
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
	defer front.proxy.Transport.(*http.Transport).CloseIdleConnections()
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
