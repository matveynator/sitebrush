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

func TestClientUsesConfiguredEndpointAndNeverSendsWrongAuthorization(t *testing.T) {
	client, err := NewClient(Config{Provider: ProviderAnthropic, BaseURL: "https://provider.invalid/v1", Model: "claude", APIKey: "secret"}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
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

func TestClientRejectsPlaintextEndpoint(t *testing.T) {
	if _, err := NewClient(Config{Provider: ProviderDeepSeek, BaseURL: "http://127.0.0.1", Model: "deepseek", APIKey: "secret"}, nil); err == nil {
		t.Fatal("plaintext provider endpoint was accepted")
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
	client, err := NewClient(Config{Provider: ProviderDeepSeek, BaseURL: "https://provider.invalid/v1", Model: "deepseek", APIKey: "secret", MaxResponseBytes: 8}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"too long"}}]}`)), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}}); err == nil {
		t.Fatal("oversized response accepted")
	}
	client, err = NewClient(Config{Provider: ProviderOpenAICompatible, BaseURL: "https://provider.invalid/v1", Model: "model", APIKey: "secret"}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
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
			client, err := NewClient(Config{Provider: ProviderDeepSeek, BaseURL: "https://provider.invalid/v1", Model: "model", APIKey: "key"}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
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
	anthropic, err := NewClient(Config{Provider: ProviderAnthropic, BaseURL: "https://provider.invalid/v1", Model: "model", APIKey: "key"}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
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
