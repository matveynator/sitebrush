package publicsite

import (
	"net/http"

	"github.com/matveynator/sitebrush/v2/pkg/requesttiming"
)

// BackendLimit bounds live dynamic sessions without queuing HTTP handlers.
// A channel token grants contact with the backend until the handler returns.
const BackendLimit = 64

type BackendAdmission struct {
	contacts chan struct{}
}

func NewBackendAdmission() *BackendAdmission {
	return &BackendAdmission{contacts: make(chan struct{}, BackendLimit)}
}

func LimitBackend(next http.Handler) http.Handler {
	return NewBackendAdmission().Wrap(next)
}

func (admission *BackendAdmission) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requesttiming.Route(request, requesttiming.ProxyApplication)
		select {
		case <-request.Context().Done():
			return
		default:
		}
		select {
		case admission.contacts <- struct{}{}:
			defer func() { <-admission.contacts }()
		default:
			writer.Header().Set("Retry-After", "1")
			writer.Header().Set("Cache-Control", "no-store")
			http.Error(writer, "application is busy; retry shortly", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
