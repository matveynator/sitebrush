package aiprovider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type streamRoundTripper func(*http.Request) (*http.Response, error)

func (roundTripper streamRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

func TestStreamOpenAICompatibleResponsesAPI(t *testing.T) {
	httpClient := &http.Client{Transport: streamRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/responses" {
			t.Fatalf("path=%q", request.URL.Path)
		}
		requestBody, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(requestBody), `"stream":true`) {
			t.Fatalf("request does not enable streaming: %s", requestBody)
		}
		body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<html>\"}\n\n" +
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok</html>\"}\n\n" +
			"data: [DONE]\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	client, err := NewClient(Config{Provider: ProviderOpenAICompatible, Model: "gpt-test", APIKey: "test-key"}, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	output := make(chan StreamChunk, 64)
	done := make(chan struct{})
	response, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "edit"}}}, output, done)
	if err != nil {
		t.Fatal(err)
	}
	close(output)
	var streamed strings.Builder
	for chunk := range output {
		streamed.WriteString(chunk.Text)
	}
	if response.Text != "<html>ok</html>" || streamed.String() != response.Text {
		t.Fatalf("response=%q streamed=%q", response.Text, streamed.String())
	}
}

func TestStreamGroqOpenAICompatibleChat(t *testing.T) {
	httpClient := &http.Client{Transport: streamRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/openai/v1/chat/completions" {
			t.Fatalf("path=%q", request.URL.Path)
		}
		body := "data: {\"choices\":[{\"delta\":{\"content\":\"Пр\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"ивет\"}}]}\n\n" +
			"data: [DONE]\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	client, err := NewClient(Config{Provider: ProviderGroq, Model: "llama-test", APIKey: "test-key"}, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	output := make(chan StreamChunk, 64)
	done := make(chan struct{})
	response, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "edit"}}}, output, done)
	if err != nil {
		t.Fatal(err)
	}
	close(output)
	var characters []string
	for chunk := range output {
		characters = append(characters, chunk.Text)
	}
	if response.Text != "Привет" {
		t.Fatalf("response=%q", response.Text)
	}
	if len(characters) != 6 {
		t.Fatalf("expected one Unicode character per channel message, got %d: %#v", len(characters), characters)
	}
}

func TestStreamOllamaAcceptsNewlineDelimitedOpenAICompatibleJSON(t *testing.T) {
	httpClient := &http.Client{Transport: streamRoundTripper(func(request *http.Request) (*http.Response, error) {
		body := "{\"choices\":[{\"delta\":{\"content\":\"<html>\"}}]}\n" +
			"{\"choices\":[{\"delta\":{\"content\":\"local</html>\"}}]}\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	client, err := NewClient(Config{Provider: ProviderOllama, Model: "qwen-test"}, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	output := make(chan StreamChunk, 64)
	done := make(chan struct{})
	response, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "edit"}}}, output, done)
	if err != nil {
		t.Fatal(err)
	}
	if response.Text != "<html>local</html>" {
		t.Fatalf("response=%q", response.Text)
	}
}

func TestStreamAnthropicTextDelta(t *testing.T) {
	httpClient := &http.Client{Transport: streamRoundTripper(func(request *http.Request) (*http.Response, error) {
		body := "event: content_block_delta\n" +
			"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	client, err := NewClient(Config{Provider: ProviderAnthropic, Model: "claude-test", APIKey: "test-key"}, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	output := make(chan StreamChunk, 64)
	done := make(chan struct{})
	response, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "edit"}}}, output, done)
	if err != nil {
		t.Fatal(err)
	}
	if response.Text != "hello" {
		t.Fatalf("response=%q", response.Text)
	}
}

func TestStreamStopsWhenConsumerClosesDone(t *testing.T) {
	httpClient := &http.Client{Transport: streamRoundTripper(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"abcdef"}}]}`)),
		}, nil
	})}
	client, err := NewClient(Config{Provider: ProviderGroq, Model: "llama-test", APIKey: "test-key"}, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	output := make(chan StreamChunk)
	done := make(chan struct{})
	close(done)
	_, err = client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "edit"}}}, output, done)
	if !errors.Is(err, ErrStreamCanceled) {
		t.Fatalf("err=%v", err)
	}
}
