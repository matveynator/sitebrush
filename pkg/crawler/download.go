package crawler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// HTMLDownloadResult carries the document and transport metadata together so
// callers can keep HTTP details out of application-level download code.
type HTMLDownloadResult struct {
	HTML        string
	IsHTML      bool
	ResolvedURL *url.URL
	Status      string
	StatusCode  int
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
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType != "" && contentType != "text/html" && contentType != "application/xhtml+xml" {
		return result, nil
	}
	pageBytes, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return result, readErr
	}
	result.HTML = DecodeHTML(pageBytes, response.Header.Get("Content-Type")).Text
	result.IsHTML = true
	return result, nil
}
