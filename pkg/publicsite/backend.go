package publicsite

import (
	"net/http"
	"strings"

	"github.com/matveynator/sitebrush/v2/pkg/requesttiming"
)

// BackendLimit bounds live dynamic sessions without queuing HTTP handlers.
// A channel token grants contact with the backend until the handler returns.
const BackendLimit = 64

// Visitor telemetry has its own bounded contact budget at both proxy boundaries.
// Long-lived browser sockets must not consume administrative admission.
const VisitorAnalyticsLimit = 128

type BackendAdmission struct {
	contacts  chan struct{}
	analytics chan struct{}
}

func NewBackendAdmission() *BackendAdmission {
	return &BackendAdmission{
		contacts:  make(chan struct{}, BackendLimit),
		analytics: make(chan struct{}, VisitorAnalyticsLimit),
	}
}

// Only the browser's standalone endpoint gets telemetry admission. Mixed
// selectors remain in the control budget because routing may select an earlier
// administrative handler. The application still validates origin and payload.
func visitorAnalyticsRequest(request *http.Request) bool {
	if request.URL == nil || request.URL.Path != "/" {
		return false
	}
	selectors := request.URL.Query()
	if len(selectors) != 1 || len(selectors["request"]) != 1 || selectors.Get("request") != "analytics" {
		return false
	}
	return request.Method == http.MethodPost || request.Method == http.MethodGet && strings.EqualFold(request.Header.Get("Upgrade"), "websocket")
}

func LimitBackend(next http.Handler) http.Handler {
	return NewBackendAdmission().Wrap(next)
}

func (admission *BackendAdmission) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requesttiming.Route(request, requesttiming.ProxyApplication)
		contacts := admission.contacts
		if visitorAnalyticsRequest(request) {
			contacts = admission.analytics
		}
		select {
		case <-request.Context().Done():
			return
		default:
		}
		select {
		case contacts <- struct{}{}:
			defer func() { <-contacts }()
		default:
			writer.Header().Set("Retry-After", "1")
			writer.Header().Set("Cache-Control", "no-store")
			http.Error(writer, "application is busy; retry shortly", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
