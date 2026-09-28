// Package aiprovider contains small, native-Go adapters for AI HTTP APIs.
package aiprovider

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/outboundhttp"
)

const (
	ProviderOpenAICompatible = "openai-compatible"
	ProviderAnthropic        = "anthropic"
	ProviderDeepSeek         = "deepseek"
	ProviderQwen             = "qwen"
	ProviderOllama           = "ollama"
	DefaultMaxResponseBytes  = 8 << 20
)

var (
	ErrProviderUnsupported  = errors.New("AI provider is unsupported")
	ErrInvalidConfiguration = errors.New("AI provider configuration is invalid")
)

type Config struct {
	Provider         string
	BaseURL          string
	Model            string
	APIKey           string
	MaxResponseBytes int64
	Timeout          time.Duration
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream,omitempty"`
}

type Response struct {
	Text string
}

type Client struct {
	configuration Config
	baseURL       string
	httpClient    *http.Client
}

func NewClient(configuration Config, httpClient *http.Client) (*Client, error) {
	configuration.Provider = strings.ToLower(strings.TrimSpace(configuration.Provider))
	if configuration.Provider == "" || strings.TrimSpace(configuration.Model) == "" || strings.TrimSpace(configuration.APIKey) == "" {
		return nil, ErrInvalidConfiguration
	}
	switch configuration.Provider {
	case ProviderOpenAICompatible, ProviderAnthropic, ProviderDeepSeek, ProviderQwen, ProviderOllama:
	default:
		return nil, fmt.Errorf("%w: %s", ErrProviderUnsupported, configuration.Provider)
	}
	baseURL, err := providerBaseURL(configuration.Provider, configuration.BaseURL)
	if err != nil {
		return nil, err
	}
	if configuration.MaxResponseBytes <= 0 {
		configuration.MaxResponseBytes = DefaultMaxResponseBytes
	}
	if configuration.Timeout <= 0 {
		configuration.Timeout = 30 * time.Second
	}
	if httpClient == nil {
		transport, transportErr := outboundhttp.NewTransport(nil, outboundhttp.TransportOptions{})
		if transportErr != nil {
			return nil, transportErr
		}
		httpClient = &http.Client{Transport: transport, Timeout: configuration.Timeout, CheckRedirect: outboundhttp.CheckRedirect}
	}
	return &Client{configuration: configuration, baseURL: baseURL, httpClient: httpClient}, nil
}

func providerBaseURL(provider, requestedBaseURL string) (string, error) {
	if provider == ProviderOllama {
		return "", fmt.Errorf("%w: Ollama is not available through the built-in public provider adapter", ErrInvalidConfiguration)
	}
	expectedBaseURL := defaultBaseURL(provider)
	requestedBaseURL = strings.TrimRight(strings.TrimSpace(requestedBaseURL), "/")
	if requestedBaseURL != "" && requestedBaseURL != expectedBaseURL {
		return "", fmt.Errorf("%w: custom AI provider endpoints are not allowed", ErrInvalidConfiguration)
	}
	return expectedBaseURL, nil
}

func defaultBaseURL(provider string) string {
	switch provider {
	case ProviderAnthropic:
		return "https://api.anthropic.com/v1"
	case ProviderDeepSeek:
		return "https://api.deepseek.com/v1"
	case ProviderQwen:
		return "https://dashscope.aliyuncs.com/compatible-mode/v1"
	default:
		return "https://api.openai.com/v1"
	}
}

func ListModels(ctx context.Context, configuration Config, httpClient *http.Client) ([]string, error) {
	configuration.Provider = strings.ToLower(strings.TrimSpace(configuration.Provider))
	configuration.APIKey = strings.TrimSpace(configuration.APIKey)
	if configuration.Provider == "" || configuration.APIKey == "" {
		return nil, ErrInvalidConfiguration
	}
	switch configuration.Provider {
	case ProviderOpenAICompatible, ProviderAnthropic, ProviderDeepSeek, ProviderQwen:
	default:
		return nil, fmt.Errorf("%w: %s", ErrProviderUnsupported, configuration.Provider)
	}
	baseURL, err := providerBaseURL(configuration.Provider, configuration.BaseURL)
	if err != nil {
		return nil, err
	}
	if configuration.MaxResponseBytes <= 0 {
		configuration.MaxResponseBytes = 2 << 20
	}
	if configuration.Timeout <= 0 {
		configuration.Timeout = 15 * time.Second
	}
	if httpClient == nil {
		transport, transportErr := outboundhttp.NewTransport(nil, outboundhttp.TransportOptions{})
		if transportErr != nil {
			return nil, transportErr
		}
		httpClient = &http.Client{Transport: transport, Timeout: configuration.Timeout, CheckRedirect: outboundhttp.CheckRedirect}
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+configuration.APIKey)
	if configuration.Provider == ProviderAnthropic {
		httpRequest.Header.Set("x-api-key", configuration.APIKey)
		httpRequest.Header.Del("Authorization")
		httpRequest.Header.Set("anthropic-version", "2023-06-01")
	}
	response, err := httpClient.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, configuration.MaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > configuration.MaxResponseBytes {
		return nil, errors.New("AI provider model response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("AI provider token validation returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(payload.Data))
	seen := make(map[string]struct{}, len(payload.Data))
	for _, model := range payload.Data {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" {
			continue
		}
		if _, exists := seen[modelID]; exists {
			continue
		}
		seen[modelID] = struct{}{}
		models = append(models, modelID)
	}
	if len(models) == 0 {
		return nil, errors.New("AI provider returned no available models")
	}
	sort.Strings(models)
	return models, nil
}

func (client *Client) Complete(ctx context.Context, request Request) (Response, error) {
	if client == nil || len(request.Messages) == 0 {
		return Response{}, ErrInvalidConfiguration
	}

	endpoint := client.baseURL + "/chat/completions"
	var payload []byte
	var err error
	if client.configuration.Provider == ProviderAnthropic {
		endpoint = client.baseURL + "/messages"
		systemParts := make([]string, 0, 2)
		conversation := make([]Message, 0, len(request.Messages))
		for _, message := range request.Messages {
			if strings.EqualFold(strings.TrimSpace(message.Role), "system") {
				systemParts = append(systemParts, message.Content)
				continue
			}
			conversation = append(conversation, message)
		}
		if len(conversation) == 0 {
			return Response{}, ErrInvalidConfiguration
		}
		payload, err = json.Marshal(struct {
			Model     string    `json:"model"`
			MaxTokens int       `json:"max_tokens"`
			System    string    `json:"system,omitempty"`
			Messages  []Message `json:"messages"`
			Stream    bool      `json:"stream,omitempty"`
		}{
			Model: client.configuration.Model,
			MaxTokens: 8192,
			System: strings.Join(systemParts, "\n\n"),
			Messages: conversation,
			Stream: request.Stream,
		})
	} else {
		payload, err = json.Marshal(struct {
			Model    string    `json:"model"`
			Messages []Message `json:"messages"`
			Stream   bool      `json:"stream,omitempty"`
		}{client.configuration.Model, request.Messages, request.Stream})
	}
	if err != nil {
		return Response{}, err
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Response{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+client.configuration.APIKey)
	if client.configuration.Provider == ProviderAnthropic {
		httpRequest.Header.Set("x-api-key", client.configuration.APIKey)
		httpRequest.Header.Del("Authorization")
		httpRequest.Header.Set("anthropic-version", "2023-06-01")
	}
	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return Response{}, err
	}
	defer response.Body.Close()
	limitedBody := io.LimitReader(response.Body, client.configuration.MaxResponseBytes+1)
	body, err := io.ReadAll(limitedBody)
	if err != nil {
		return Response{}, err
	}
	if int64(len(body)) > client.configuration.MaxResponseBytes {
		return Response{}, errors.New("AI provider response is too large")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Response{}, fmt.Errorf("AI provider returned HTTP %d", response.StatusCode)
	}
	if client.configuration.Provider == ProviderAnthropic {
		var anthropicResponse struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(body, &anthropicResponse); err != nil {
			return Response{}, err
		}
		if len(anthropicResponse.Content) == 0 {
			return Response{}, errors.New("AI provider response has no content")
		}
		return Response{Text: anthropicResponse.Content[0].Text}, nil
	}
	var compatibleResponse struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &compatibleResponse); err != nil {
		return Response{}, err
	}
	if len(compatibleResponse.Choices) == 0 {
		return Response{}, errors.New("AI provider response has no choices")
	}
	return Response{Text: compatibleResponse.Choices[0].Message.Content}, nil
}

func EncryptSecret(key []byte, secret string) (string, error) {
	if len(key) != 32 || secret == "" {
		return "", errors.New("AES-256 key and secret are required")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nonce, nonce, []byte(secret), nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

func DecryptSecret(key []byte, encoded string) (string, error) {
	if len(key) != 32 || encoded == "" {
		return "", errors.New("AES-256 key and ciphertext are required")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(ciphertext) < aead.NonceSize() {
		return "", errors.New("invalid encrypted secret")
	}
	plaintext, err := aead.Open(nil, ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():], nil)
	if err != nil {
		return "", errors.New("invalid encrypted secret")
	}
	return string(plaintext), nil
}
