package crawler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/outboundhttp"
)

const maxHTMLDownloadBytes int64 = 32 * 1024 * 1024

// HTMLDownloadResult carries the document and transport metadata together so
// callers can keep HTTP details out of application-level download code.
type HTMLDownloadResult struct {
	HTML            string
	IsHTML          bool
	ResolvedURL     *url.URL
	Status          string
	StatusCode      int
	Encoding        string
	EncodingSource  string
	EncodingCertain bool
}

type HTMLDownloadRetryOptions struct {
	Attempts  int
	Delay     time.Duration
	OnAttempt func(attempt, total int, pageURL *url.URL)
	OnRetry   func(attempt, total int, pageURL *url.URL, err error, delay time.Duration)
}

func NewSessionClient(timeout time.Duration, transport http.RoundTripper) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Timeout: timeout, Transport: transport, Jar: jar}
}

func DownloadHTML(client *http.Client, pageURL *url.URL, applyHeaders func(*http.Request)) (string, bool, error) {
	return DownloadHTMLContext(context.Background(), client, pageURL, applyHeaders)
}

func DownloadHTMLContext(ctx context.Context, client *http.Client, pageURL *url.URL, applyHeaders func(*http.Request)) (string, bool, error) {
	result, err := DownloadHTMLPageContext(ctx, client, pageURL, applyHeaders)
	return result.HTML, result.IsHTML, err
}

func DownloadHTMLPageContext(ctx context.Context, client *http.Client, pageURL *url.URL, applyHeaders func(*http.Request)) (HTMLDownloadResult, error) {
	if client == nil {
		return HTMLDownloadResult{}, errors.New("missing http client")
	}
	if pageURL == nil || pageURL.String() == "" {
		return HTMLDownloadResult{}, errors.New("missing page url")
	}
	if err := outboundhttp.RequirePublicURL(pageURL); err != nil {
		return HTMLDownloadResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), nil)
	if err != nil {
		return HTMLDownloadResult{}, err
	}
	if applyHeaders != nil {
		applyHeaders(request)
	}
	response, err := client.Do(request)
	if err != nil {
		return HTMLDownloadResult{}, err
	}
	defer response.Body.Close()
	result := HTMLDownloadResult{
		ResolvedURL: pageURL,
		Status:      response.Status,
		StatusCode:  response.StatusCode,
	}
	if response.Request != nil && response.Request.URL != nil {
		result.ResolvedURL = response.Request.URL
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return result, fmt.Errorf("page download failed: %s", response.Status)
	}
	pageBytes, readErr := readHTMLBodyWithLimit(response.Body, maxHTMLDownloadBytes)
	if readErr != nil {
		return result, readErr
	}
	contentType := DetectedResourceContentType(pageURL.String(), response.Header.Get("Content-Type"), pageBytes)
	if contentType != "text/html" && contentType != "application/xhtml+xml" {
		return result, nil
	}
	decodedHTML := DecodeHTML(pageBytes, response.Header.Get("Content-Type"))
	result.HTML = decodedHTML.Text
	result.Encoding = decodedHTML.Encoding
	result.EncodingSource = decodedHTML.EncodingSource
	result.EncodingCertain = decodedHTML.Certain
	result.IsHTML = true
	return result, nil
}

func DownloadHTMLPageWithRetriesContext(ctx context.Context, client *http.Client, pageURL *url.URL, applyHeaders func(*http.Request), options HTMLDownloadRetryOptions) (HTMLDownloadResult, error) {
	attempts := options.Attempts
	if attempts < 1 {
		attempts = 1
	}
	delay := options.Delay
	if delay < 0 {
		delay = 0
	}
	var lastResult HTMLDownloadResult
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if options.OnAttempt != nil {
			options.OnAttempt(attempt, attempts, pageURL)
		}
		result, err := DownloadHTMLPageContext(ctx, client, pageURL, applyHeaders)
		if err == nil && result.IsHTML {
			return result, nil
		}
		lastResult = result
		if err != nil {
			lastErr = err
		} else {
			lastErr = errors.New("page did not return a public HTML page")
		}
		if attempt >= attempts {
			break
		}
		if options.OnRetry != nil {
			options.OnRetry(attempt, attempts, pageURL, lastErr, delay)
		}
		if delay <= 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return lastResult, ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastResult, lastErr
}

func readHTMLBodyWithLimit(reader io.Reader, limitBytes int64) ([]byte, error) {
	if limitBytes <= 0 {
		return io.ReadAll(reader)
	}
	pageBytes, err := io.ReadAll(io.LimitReader(reader, limitBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(pageBytes)) > limitBytes {
		return nil, fmt.Errorf("html body exceeds %d bytes", limitBytes)
	}
	return pageBytes, nil
}
