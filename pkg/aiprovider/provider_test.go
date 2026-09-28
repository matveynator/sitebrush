package aiprovider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestClientUsesProviderEndpointAndNeverSendsWrongAuthorization(t *testing.T) {
	client, err := NewClient(Config{Provider: ProviderAnthropic, Model: "claude", APIKey: "secret"}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://api.anthropic.com/v1/chat/completions" {
			return nil, errors.New("unexpected Anthropic endpoint")
		}
		if request.Header.Get("x-api-key") != "secret" || request.Header.Get("Authorization") != "" {
			return nil, errors.New("unexpected Anthropic headers")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"content":[{"text":"ok"}]}`)), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hello"}}})
	if err != nil || result.Text != "ok" {
		t.Fatalf("result=%q err=%v", result.Text, err)
	}
}

func TestClientRejectsCustomProviderEndpoint(t *testing.T) {
	for _, baseURL := range []string{"http://127.0.0.1", "https://provider.invalid/v1", "https://api.deepseek.com/v1/other"} {
		if _, err := NewClient(Config{Provider: ProviderDeepSeek, BaseURL: baseURL, Model: "deepseek", APIKey: "secret"}, nil); err == nil {
			t.Fatalf("custom provider endpoint %q was accepted", baseURL)
		}
	}
	client, err := NewClient(Config{Provider: ProviderDeepSeek, BaseURL: "https://api.deepseek.com/v1/", Model: "deepseek", APIKey: "secret"}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://api.deepseek.com/v1/chat/completions" {
			return nil, errors.New("unexpected DeepSeek endpoint")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatalf("official provider endpoint rejected: %v", err)
	}
	if _, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}}); err != nil {
		t.Fatalf("official provider request failed: %v", err)
	}
}

func TestSecretEncryptionRoundTripAndTamperRejection(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	ciphertext, err := EncryptSecret(key, "SUPER_SECRET_MARKER")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := DecryptSecret(key, ciphertext)
	if err != nil || plaintext != "SUPER_SECRET_MARKER" {
		t.Fatalf("plaintext=%q err=%v", plaintext, err)
	}
	if _, err := DecryptSecret(key, ciphertext+"x"); err == nil {
		t.Fatal("tampered secret was accepted")
	}
}

func TestOpenAICompatibleResponseErrorsAndLimits(t *testing.T) {
	client, err := NewClient(Config{Provider: ProviderDeepSeek, Model: "deepseek", APIKey: "secret", MaxResponseBytes: 8}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"too long"}}]}`)), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}}); err == nil {
		t.Fatal("oversized response accepted")
	}
	client, err = NewClient(Config{Provider: ProviderOpenAICompatible, Model: "model", APIKey: "secret"}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("failure")), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}}); err == nil {
		t.Fatal("provider error accepted")
	}
}

func TestProviderConfigurationAndDecryptionErrors(t *testing.T) {
	if _, err := NewClient(Config{Provider: "unknown", Model: "model", APIKey: "secret"}, nil); err == nil {
		t.Fatal("unknown provider accepted")
	}
	if _, err := EncryptSecret([]byte("short"), "secret"); err == nil {
		t.Fatal("short encryption key accepted")
	}
	if _, err := DecryptSecret([]byte(strings.Repeat("k", 32)), "invalid"); err == nil {
		t.Fatal("invalid ciphertext accepted")
	}
	if _, err := EncryptSecret([]byte(strings.Repeat("k", 32)), ""); err == nil {
		t.Fatal("empty secret accepted")
	}
	encoded, err := EncryptSecret([]byte(strings.Repeat("k", 32)), "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptSecret([]byte(strings.Repeat("z", 32)), encoded); err == nil {
		t.Fatal("wrong decryption key accepted")
	}
}

func TestOllamaIsRejectedByBuiltInPublicProviderAdapter(t *testing.T) {
	for _, baseURL := range []string{"", "https://provider.invalid/v1"} {
		if _, err := NewClient(Config{Provider: ProviderOllama, BaseURL: baseURL, Model: "model", APIKey: "key"}, nil); err == nil {
			t.Fatalf("Ollama endpoint %q was accepted by the built-in public provider adapter", baseURL)
		}
	}
}

func TestDefaultProviderURLsAndOpenAIResponse(t *testing.T) {
	for _, provider := range []string{ProviderOpenAICompatible, ProviderAnthropic, ProviderDeepSeek, ProviderQwen} {
		client, err := NewClient(Config{Provider: provider, Model: "model", APIKey: "key"}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)), Header: make(http.Header)}, nil
		})})
		if err != nil {
			t.Fatalf("provider=%s err=%v", provider, err)
		}
		if provider != ProviderAnthropic {
			result, completeErr := client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hello"}}})
			if completeErr != nil || result.Text != "ok" {
				t.Fatalf("provider=%s result=%q err=%v", provider, result.Text, completeErr)
			}
		}
	}
}

func TestProviderResponseValidationAndTransportErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "invalid JSON", body: "{", want: "unexpected end"},
		{name: "missing choices", body: `{}`, want: "no choices"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client, err := NewClient(Config{Provider: ProviderDeepSeek, Model: "model", APIKey: "key"}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(testCase.body)), Header: make(http.Header)}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}}); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error=%v, want %q", err, testCase.want)
			}
		})
	}
	anthropic, err := NewClient(Config{Provider: ProviderAnthropic, Model: "model", APIKey: "key"}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := anthropic.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}}); err == nil {
		t.Fatal("empty Anthropic content accepted")
	}
	if _, err := (*Client)(nil).Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("nil client error=%v", err)
	}
}


type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingReadCloser) Close() error              { return nil }

func TestProviderRejectsIncompleteConfiguration(t *testing.T) {
	for _, configuration := range []Config{
		{},
		{Provider: ProviderDeepSeek, APIKey: "key"},
		{Provider: ProviderDeepSeek, Model: "model"},
		{Provider: "  ", Model: "model", APIKey: "key"},
	} {
		if _, err := NewClient(configuration, nil); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("configuration=%+v error=%v", configuration, err)
		}
	}
}

func TestProviderTransportAndBodyReadErrorsAreReturned(t *testing.T) {
	transportErr := errors.New("transport failed")
	client, err := NewClient(Config{Provider: ProviderDeepSeek, Model: "model", APIKey: "key"}, &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, transportErr
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}}); !errors.Is(err, transportErr) {
		t.Fatalf("transport error=%v", err)
	}

	client, err = NewClient(Config{Provider: ProviderDeepSeek, Model: "model", APIKey: "key"}, &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: failingReadCloser{}, Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}}); err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("body read error=%v", err)
	}
}

func TestProviderRejectsEmptyRequestAndMalformedAnthropicJSON(t *testing.T) {
	client, err := NewClient(Config{Provider: ProviderAnthropic, Model: "model", APIKey: "key"}, &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{")), Header: make(http.Header)}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), Request{}); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("empty request error=%v", err)
	}
	if _, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}}); err == nil {
		t.Fatal("malformed Anthropic JSON accepted")
	}
}

func TestProviderDefaultURLs(t *testing.T) {
	expected := map[string]string{
		ProviderOpenAICompatible: "https://api.openai.com/v1",
		ProviderAnthropic:        "https://api.anthropic.com/v1",
		ProviderDeepSeek:         "https://api.deepseek.com/v1",
		ProviderQwen:             "https://dashscope.aliyuncs.com/compatible-mode/v1",
	}
	for provider, expectedURL := range expected {
		if actual := defaultBaseURL(provider); actual != expectedURL {
			t.Fatalf("provider=%s URL=%q want=%q", provider, actual, expectedURL)
		}
		if actual, err := providerBaseURL(provider, expectedURL+"/"); err != nil || actual != expectedURL {
			t.Fatalf("provider=%s explicit official URL=%q err=%v", provider, actual, err)
		}
	}
}

func TestSecretDecryptionRejectsInvalidKeyAndEmptyCiphertext(t *testing.T) {
	if _, err := DecryptSecret([]byte("short"), "value"); err == nil {
		t.Fatal("short decryption key accepted")
	}
	if _, err := DecryptSecret([]byte(strings.Repeat("k", 32)), ""); err == nil {
		t.Fatal("empty ciphertext accepted")
	}
}
