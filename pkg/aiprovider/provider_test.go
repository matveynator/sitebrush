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

func TestAnthropicCompleteSeparatesSystemPrompt(t *testing.T) {
	client, err := NewClient(Config{Provider: ProviderAnthropic, Model: "claude-sonnet-5", APIKey: "secret"}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			return nil, readErr
		}
		if request.URL.String() != "https://api.anthropic.com/v1/messages" {
			return nil, errors.New("unexpected Anthropic endpoint")
		}
		if !strings.Contains(string(body), `"system":"system instructions"`) || strings.Contains(string(body), `"role":"system"`) {
			return nil, errors.New("Anthropic system prompt was not separated")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"content":[{"text":"ok"}]}`)), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Complete(context.Background(), Request{Messages: []Message{
		{Role: "system", Content: "system instructions"},
		{Role: "user", Content: "hello"},
	}})
	if err != nil || response.Text != "ok" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestClientUsesProviderEndpointAndNeverSendsWrongAuthorization(t *testing.T) {
	client, err := NewClient(Config{Provider: ProviderAnthropic, Model: "claude", APIKey: "secret"}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://api.anthropic.com/v1/messages" {
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

func TestOllamaUsesFixedLoopbackEndpointWithoutAPIKey(t *testing.T) {
	client, err := NewClient(Config{Provider: ProviderOllama, Model: "qwen3"}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "http://127.0.0.1:11434/v1/chat/completions" {
			return nil, errors.New("unexpected Ollama endpoint")
		}
		if request.Header.Get("Authorization") != "" {
			return nil, errors.New("Ollama request unexpectedly carried authorization")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hello"}}})
	if err != nil || result.Text != "ok" {
		t.Fatalf("result=%q err=%v", result.Text, err)
	}
	for _, baseURL := range []string{"http://localhost:11434/v1", "http://127.0.0.1:11435/v1", "https://provider.invalid/v1"} {
		if _, err := NewClient(Config{Provider: ProviderOllama, BaseURL: baseURL, Model: "qwen3"}, nil); err == nil {
			t.Fatalf("custom Ollama endpoint %q was accepted", baseURL)
		}
	}
}

func TestDefaultProviderURLsAndOpenAIResponse(t *testing.T) {
	for _, provider := range []string{ProviderOpenAICompatible, ProviderAnthropic, ProviderDeepSeek, ProviderQwen} {
		currentProvider := provider
		client, err := NewClient(Config{Provider: currentProvider, Model: "model", APIKey: "key"}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			body := `{"choices":[{"message":{"content":"ok"}}]}`
			if currentProvider == ProviderOpenAICompatible {
				body = `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
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
		ProviderGemini:           "https://generativelanguage.googleapis.com/v1beta/openai",
		ProviderGroq:             "https://api.groq.com/openai/v1",
		ProviderMistral:          "https://api.mistral.ai/v1",
		ProviderOllama:           "http://127.0.0.1:11434/v1",
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


func TestListModelsValidatesProviderTokenAndReturnsUniqueModels(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "https://api.openai.com/v1/models" {
			return nil, errors.New("unexpected models request")
		}
		if request.Header.Get("Authorization") != "Bearer secret" {
			return nil, errors.New("missing bearer token")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-6-sol"},{"id":"gpt-6-luna"},{"id":"gpt-6-sol"}]}`)),
			Header: make(http.Header),
		}, nil
	})}
	models, err := ListModels(context.Background(), Config{Provider: ProviderOpenAICompatible, APIKey: "secret"}, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "gpt-6-luna" || models[1] != "gpt-6-sol" {
		t.Fatalf("models=%v", models)
	}
}

func TestListModelsUsesAnthropicHeadersAndRejectsInvalidToken(t *testing.T) {
	var requests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.String() != "https://api.anthropic.com/v1/models" {
			return nil, errors.New("unexpected Anthropic models endpoint")
		}
		if request.Header.Get("x-api-key") != "secret" || request.Header.Get("Authorization") != "" {
			return nil, errors.New("unexpected Anthropic auth headers")
		}
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body: io.NopCloser(strings.NewReader(`{"error":"invalid"}`)),
			Header: make(http.Header),
		}, nil
	})}
	if _, err := ListModels(context.Background(), Config{Provider: ProviderAnthropic, APIKey: "secret"}, client); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("invalid token error=%v", err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d", requests)
	}
}

func TestListModelsRejectsMissingConfigurationAndEmptyModelList(t *testing.T) {
	if _, err := ListModels(context.Background(), Config{}, nil); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("missing configuration error=%v", err)
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{"data":[]}`)),
			Header: make(http.Header),
		}, nil
	})}
	if _, err := ListModels(context.Background(), Config{Provider: ProviderDeepSeek, APIKey: "secret"}, client); err == nil {
		t.Fatal("empty model list was accepted")
	}
}


func TestSecretEncryptionContextPreventsCredentialRebinding(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	ciphertext, err := EncryptSecretWithContext(key, "PROVIDER_SECRET", "example.org\nowner@example.org\nopenai-compatible")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := DecryptSecretWithContext(key, ciphertext, "example.org\nowner@example.org\nopenai-compatible")
	if err != nil || plaintext != "PROVIDER_SECRET" {
		t.Fatalf("plaintext=%q err=%v", plaintext, err)
	}
	for _, wrongContext := range []string{
		"example.org\nowner@example.org\ndeepseek",
		"example.org\nother@example.org\nopenai-compatible",
		"other.example\nowner@example.org\nopenai-compatible",
	} {
		if _, err := DecryptSecretWithContext(key, ciphertext, wrongContext); err == nil {
			t.Fatalf("SECURITY: encrypted provider credential accepted rebound context %q", wrongContext)
		}
	}
}


func TestOpenAICompatibleUsesResponsesAPI(t *testing.T) {
	client, err := NewClient(Config{Provider: ProviderOpenAICompatible, Model: "gpt-5.6-terra", APIKey: "secret"}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://api.openai.com/v1/responses" {
			return nil, errors.New("unexpected OpenAI Responses endpoint")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(string(body), `"instructions":"system instructions"`) || !strings.Contains(string(body), `"input":[{"role":"user","content":"hello"}]`) {
			return nil, errors.New("unexpected OpenAI Responses payload")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)),
			Header: make(http.Header),
		}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Complete(context.Background(), Request{Messages: []Message{
		{Role: "system", Content: "system instructions"},
		{Role: "user", Content: "hello"},
	}})
	if err != nil || result.Text != "ok" {
		t.Fatalf("result=%q err=%v", result.Text, err)
	}
}

func TestProviderHTTPErrorDoesNotEchoResponseBody(t *testing.T) {
	client, err := NewClient(Config{Provider: ProviderDeepSeek, Model: "deepseek-chat", APIKey: "secret"}, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body: io.NopCloser(strings.NewReader(`{"error":"PRIVATE_PROVIDER_DETAILS"}`)),
			Header: make(http.Header),
		}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "x"}}})
	var providerErr *HTTPError
	if !errors.As(err, &providerErr) || providerErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("error=%T %v", err, err)
	}
	if strings.Contains(err.Error(), "PRIVATE_PROVIDER_DETAILS") {
		t.Fatal("provider response body leaked through error")
	}
}


func TestFreeTierProviderEndpointsUseBearerAuthentication(t *testing.T) {
	expected := map[string]string{
		ProviderGemini:  "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
		ProviderGroq:    "https://api.groq.com/openai/v1/chat/completions",
		ProviderMistral: "https://api.mistral.ai/v1/chat/completions",
	}
	for provider, endpoint := range expected {
		t.Run(provider, func(t *testing.T) {
			client, err := NewClient(Config{Provider: provider, Model: "test-model", APIKey: "secret"}, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() != endpoint {
					return nil, errors.New("unexpected provider endpoint")
				}
				if request.Header.Get("Authorization") != "Bearer secret" {
					return nil, errors.New("missing bearer token")
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)),
					Header: make(http.Header),
				}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hello"}}})
			if err != nil || result.Text != "ok" {
				t.Fatalf("result=%q err=%v", result.Text, err)
			}
		})
	}
}


func TestListModelsSupportsDirectArrayResponses(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://api.mistral.ai/v1/models" {
			return nil, errors.New("unexpected Mistral models endpoint")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`[{"id":"mistral-small-latest"},{"id":"mistral-large-latest"}]`)),
			Header: make(http.Header),
		}, nil
	})}
	models, err := ListModels(context.Background(), Config{Provider: ProviderMistral, APIKey: "secret"}, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "mistral-large-latest" || models[1] != "mistral-small-latest" {
		t.Fatalf("models=%v", models)
	}
}

func TestListModelsReturnsTypedHTTPError(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body: io.NopCloser(strings.NewReader(`{"error":"secret details"}`)),
			Header: make(http.Header),
		}, nil
	})}
	_, err := ListModels(context.Background(), Config{Provider: ProviderGemini, APIKey: "bad-key"}, client)
	var providerErr *HTTPError
	if !errors.As(err, &providerErr) || providerErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error=%T %v", err, err)
	}
	if strings.Contains(err.Error(), "secret details") {
		t.Fatal("provider error body leaked")
	}
}


func TestListModelsSupportsLocalOllamaWithoutAuthorization(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "http://127.0.0.1:11434/v1/models" {
			return nil, errors.New("unexpected Ollama models endpoint")
		}
		if request.Header.Get("Authorization") != "" {
			return nil, errors.New("Ollama models request unexpectedly carried authorization")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"qwen3:8b"},{"id":"llama3.2:latest"}]}`)),
			Header: make(http.Header),
		}, nil
	})}
	models, err := ListModels(context.Background(), Config{Provider: ProviderOllama}, client)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "llama3.2:latest" || models[1] != "qwen3:8b" {
		t.Fatalf("models=%v", models)
	}
}


func TestOllamaCompleteUsesNativeChatAPIAndDisablesThinking(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "http://127.0.0.1:11434/api/chat" {
			t.Fatalf("endpoint=%q", request.URL.String())
		}
		if request.Header.Get("Authorization") != "" {
			t.Fatal("native Ollama request unexpectedly carried authorization")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		bodyText := string(body)
		for _, required := range []string{
			`"model":"qwen3:8b"`,
			`"role":"system"`,
			`"role":"user"`,
			`"stream":false`,
			`"think":false`,
			`"num_ctx":65536`,
		} {
			if !strings.Contains(bodyText, required) {
				t.Fatalf("Ollama payload missing %q: %s", required, bodyText)
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"message":{"role":"assistant","content":"SITEBRUSH_EDIT\n<html><body>done</body></html>"},"done":true}`)),
		}, nil
	})}
	client, err := NewClient(Config{Provider: ProviderOllama, Model: "qwen3:8b"}, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Complete(context.Background(), Request{Messages: []Message{
		{Role: "system", Content: "edit, do not chat"},
		{Role: "user", Content: "task at prompt tail"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "SITEBRUSH_EDIT") {
		t.Fatalf("result=%q", result.Text)
	}
}
