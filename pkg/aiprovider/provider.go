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
	ProviderGemini           = "gemini"
	ProviderGroq             = "groq"
	ProviderMistral          = "mistral"
	ProviderOllama           = "ollama"
	DefaultMaxResponseBytes  = 8 << 20
)

var (
	ErrProviderUnsupported  = errors.New("AI provider is unsupported")
	ErrInvalidConfiguration = errors.New("AI provider configuration is invalid")
)

type HTTPError struct {
	StatusCode int
	Message    string
}

func (err *HTTPError) Error() string {
	if err == nil {
		return "AI provider request failed"
	}
	if strings.TrimSpace(err.Message) == "" {
		return fmt.Sprintf("AI provider returned HTTP %d", err.StatusCode)
	}
	return fmt.Sprintf("AI provider returned HTTP %d: %s", err.StatusCode, err.Message)
}

func newHTTPError(statusCode int, body []byte) *HTTPError {
	return &HTTPError{StatusCode: statusCode, Message: providerHTTPErrorMessage(body)}
}

func providerHTTPErrorMessage(body []byte) string {
	trimmedBody := strings.TrimSpace(string(body))
	if trimmedBody == "" {
		return ""
	}
	var objectError struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &objectError) == nil {
		if strings.TrimSpace(objectError.Error.Message) != "" {
			trimmedBody = objectError.Error.Message
		} else if strings.TrimSpace(objectError.Message) != "" {
			trimmedBody = objectError.Message
		}
	} else {
		var stringError struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &stringError) == nil && strings.TrimSpace(stringError.Error) != "" {
			trimmedBody = stringError.Error
		}
	}
	trimmedBody = strings.Join(strings.Fields(trimmedBody), " ")
	if len(trimmedBody) > 512 {
		trimmedBody = trimmedBody[:512] + "…"
	}
	return trimmedBody
}

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
	if configuration.Provider == "" || strings.TrimSpace(configuration.Model) == "" {
		return nil, ErrInvalidConfiguration
	}
	if configuration.Provider != ProviderOllama && strings.TrimSpace(configuration.APIKey) == "" {
		return nil, ErrInvalidConfiguration
	}
	switch configuration.Provider {
	case ProviderOpenAICompatible, ProviderAnthropic, ProviderDeepSeek, ProviderQwen, ProviderGemini, ProviderGroq, ProviderMistral, ProviderOllama:
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
		if configuration.Provider == ProviderOllama {
			// Local models may need minutes to load weights and produce the first
			// token. Keep that latency at the provider boundary instead of turning
			// a slow but healthy Ollama instance into a false editor failure.
			configuration.Timeout = 15 * time.Minute
		} else {
			configuration.Timeout = 2 * time.Minute
		}
	}
	if httpClient == nil {
		if configuration.Provider == ProviderOllama {
			httpClient = newOllamaHTTPClient(configuration.Timeout)
		} else {
			transport, transportErr := outboundhttp.NewTransport(nil, outboundhttp.TransportOptions{})
			if transportErr != nil {
				return nil, transportErr
			}
			httpClient = &http.Client{Transport: transport, Timeout: configuration.Timeout, CheckRedirect: outboundhttp.CheckRedirect}
		}
	}
	return &Client{configuration: configuration, baseURL: baseURL, httpClient: httpClient}, nil
}

func providerBaseURL(provider, requestedBaseURL string) (string, error) {
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
	case ProviderGemini:
		return "https://generativelanguage.googleapis.com/v1beta/openai"
	case ProviderGroq:
		return "https://api.groq.com/openai/v1"
	case ProviderMistral:
		return "https://api.mistral.ai/v1"
	case ProviderOllama:
		return "http://127.0.0.1:11434/v1"
	default:
		return "https://api.openai.com/v1"
	}
}

func newOllamaHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("Ollama redirects are not allowed")
		},
	}
}

func ListModels(ctx context.Context, configuration Config, httpClient *http.Client) ([]string, error) {
	configuration.Provider = strings.ToLower(strings.TrimSpace(configuration.Provider))
	configuration.APIKey = strings.TrimSpace(configuration.APIKey)
	if configuration.Provider == "" {
		return nil, ErrInvalidConfiguration
	}
	if configuration.Provider != ProviderOllama && configuration.APIKey == "" {
		return nil, ErrInvalidConfiguration
	}
	switch configuration.Provider {
	case ProviderOpenAICompatible, ProviderAnthropic, ProviderDeepSeek, ProviderQwen, ProviderGemini, ProviderGroq, ProviderMistral, ProviderOllama:
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
		if configuration.Provider == ProviderOllama {
			httpClient = newOllamaHTTPClient(configuration.Timeout)
		} else {
			transport, transportErr := outboundhttp.NewTransport(nil, outboundhttp.TransportOptions{})
			if transportErr != nil {
				return nil, transportErr
			}
			httpClient = &http.Client{Transport: transport, Timeout: configuration.Timeout, CheckRedirect: outboundhttp.CheckRedirect}
		}
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Accept", "application/json")
	if configuration.Provider != ProviderOllama {
		httpRequest.Header.Set("Authorization", "Bearer "+configuration.APIKey)
	}
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
		return nil, newHTTPError(response.StatusCode, body)
	}
	type modelRecord struct {
		ID string `json:"id"`
	}
	var payload struct {
		Data []modelRecord `json:"data"`
	}
	modelRecords := []modelRecord(nil)
	if err := json.Unmarshal(body, &payload); err == nil && len(payload.Data) != 0 {
		modelRecords = payload.Data
	} else {
		var directModels []modelRecord
		if directErr := json.Unmarshal(body, &directModels); directErr != nil {
			if err != nil {
				return nil, err
			}
			return nil, directErr
		}
		modelRecords = directModels
	}
	models := make([]string, 0, len(modelRecords))
	seen := make(map[string]struct{}, len(modelRecords))
	for _, model := range modelRecords {
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
	switch client.configuration.Provider {
	case ProviderOpenAICompatible:
		endpoint = client.baseURL + "/responses"
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
			Model        string    `json:"model"`
			Instructions string    `json:"instructions,omitempty"`
			Input        []Message `json:"input"`
		}{
			Model:        client.configuration.Model,
			Instructions: strings.Join(systemParts, "\n\n"),
			Input:        conversation,
		})
	case ProviderAnthropic:
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
	default:
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
	if client.configuration.Provider != ProviderOllama {
		httpRequest.Header.Set("Authorization", "Bearer "+client.configuration.APIKey)
	}
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
		return Response{}, newHTTPError(response.StatusCode, body)
	}
	if client.configuration.Provider == ProviderOpenAICompatible {
		var openAIResponse struct {
			Output []struct {
				Type    string `json:"type"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
		}
		if err := json.Unmarshal(body, &openAIResponse); err != nil {
			return Response{}, err
		}
		var textParts []string
		for _, output := range openAIResponse.Output {
			if output.Type != "" && output.Type != "message" {
				continue
			}
			for _, content := range output.Content {
				if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
					textParts = append(textParts, content.Text)
				}
			}
		}
		if len(textParts) == 0 {
			return Response{}, errors.New("AI provider response has no output text")
		}
		return Response{Text: strings.Join(textParts, "")}, nil
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

func EncryptSecretWithContext(key []byte, secret, secretContext string) (string, error) {
	if len(key) != 32 || secret == "" || strings.TrimSpace(secretContext) == "" {
		return "", errors.New("AES-256 key, secret, and context are required")
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
	ciphertext := aead.Seal(nonce, nonce, []byte(secret), []byte(secretContext))
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

func DecryptSecretWithContext(key []byte, encoded, secretContext string) (string, error) {
	if len(key) != 32 || encoded == "" || strings.TrimSpace(secretContext) == "" {
		return "", errors.New("AES-256 key, ciphertext, and context are required")
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
	plaintext, err := aead.Open(nil, ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():], []byte(secretContext))
	if err != nil {
		return "", errors.New("invalid encrypted secret")
	}
	return string(plaintext), nil
}

