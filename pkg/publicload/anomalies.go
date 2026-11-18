package publicload

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"unicode/utf8"
)

const maximumErrorSamples = 16
const maximumErrorBodyPrefix = 1024

type ResponseAnomaly struct {
	Reason             string
	Status             int
	Protocol           string
	DeclaredBytes      int64
	ExpectedBytes      int64
	ActualBytes        int64
	ETag               string
	SHA256             string
	ReadError          string
	BodyPrefix         string
	BodyPrefixEncoding string
}

// One worker reuses its own hash and prefix buffer. Only a failed response may
// consume one of the run's finite sample tickets and transfer a copied prefix.
type anomalyBody struct {
	tickets <-chan struct{}
	digest  hash.Hash
	prefix  [maximumErrorBodyPrefix]byte
	used    int
}

func newAnomalyBody(tickets <-chan struct{}) *anomalyBody {
	return &anomalyBody{digest: sha256.New(), tickets: tickets}
}

func (body *anomalyBody) reset() { body.digest.Reset(); body.used = 0 }

func (body *anomalyBody) Write(payload []byte) (int, error) {
	body.used += copy(body.prefix[body.used:], payload)
	return body.digest.Write(payload)
}

func anomalyReason(result sample, options Options) string {
	if result.error != "" {
		return "transport"
	}
	if result.status < 200 || result.status >= 300 {
		return "status"
	}
	if result.declaredBytes >= 0 && result.bytes != result.declaredBytes {
		return "declared_length"
	}
	if options.ExpectedBytes > 0 && result.bytes != options.ExpectedBytes {
		return "length"
	}
	if options.ExpectedSHA256 != "" && result.digest != options.ExpectedSHA256 {
		return "content"
	}
	return ""
}

func captureAnomaly(result sample, options Options, capture *anomalyBody) *ResponseAnomaly {
	if capture == nil {
		return nil
	}
	reason := anomalyReason(result, options)
	if reason == "" {
		return nil
	}
	// Tickets are consumed, never closed or returned, so all concurrent workers
	// together can retain at most ErrorSamples bodies for this run.
	select {
	case <-capture.tickets:
	default:
		return nil
	}
	prefix := capture.prefix[:capture.used]
	anomaly := &ResponseAnomaly{Reason: reason, Status: result.status, Protocol: result.protocol, DeclaredBytes: result.declaredBytes, ExpectedBytes: options.ExpectedBytes, ActualBytes: result.bytes, ETag: result.etag, SHA256: result.digest, ReadError: result.error, BodyPrefixEncoding: "utf-8"}
	if utf8.Valid(prefix) {
		anomaly.BodyPrefix = string(prefix)
	} else {
		anomaly.BodyPrefix = hex.EncodeToString(prefix)
		anomaly.BodyPrefixEncoding = "hex"
	}
	return anomaly
}
