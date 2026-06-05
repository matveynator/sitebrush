package crawler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func DownloadHTML(client *http.Client, pageURL *url.URL, applyHeaders func(*http.Request)) (string, bool, error) {
	return DownloadHTMLContext(context.Background(), client, pageURL, applyHeaders)
}

func DownloadHTMLContext(ctx context.Context, client *http.Client, pageURL *url.URL, applyHeaders func(*http.Request)) (string, bool, error) {
	if client == nil {
		return "", false, errors.New("missing http client")
	}
	if pageURL == nil || pageURL.String() == "" {
		return "", false, errors.New("missing page url")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), nil)
	if err != nil {
		return "", false, err
	}
	if applyHeaders != nil {
		applyHeaders(request)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", false, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return "", false, fmt.Errorf("page download failed: %s", response.Status)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType != "" && contentType != "text/html" && contentType != "application/xhtml+xml" {
		return "", false, nil
	}
	pageBytes, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return "", false, readErr
	}
	return DecodeHTML(pageBytes, response.Header.Get("Content-Type")).Text, true, nil
}
