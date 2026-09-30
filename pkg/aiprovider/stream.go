// Package aiprovider contains small, native-Go adapters for AI HTTP APIs.
package aiprovider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

var ErrStreamCanceled = errors.New("AI provider stream canceled")

// StreamChunk transfers provider output through a channel so callers can keep
// long-running inference outside their request/UI state.
type StreamChunk struct {
	Text string
}

// Stream performs one provider request and emits completed Unicode characters
// as they arrive. The done channel is owned by the consumer and is the internal
// cancellation boundary; context is used only at the outbound HTTP boundary.
func (client *Client) Stream(ctx context.Context, request Request, output chan<- StreamChunk, done <-chan struct{}) (Response, error) {
	if client == nil || len(request.Messages) == 0 || output == nil || done == nil {
		return Response{}, ErrInvalidConfiguration
	}

	endpoint, payload, err := client.streamingRequestPayload(request)
	if err != nil {
		return Response{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return Response{}, err
	}
	client.applyProviderHeaders(httpRequest)

	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return Response{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		errorBody, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		return Response{}, newHTTPError(response.StatusCode, errorBody)
	}

	if !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, client.configuration.MaxResponseBytes+1))
		if readErr != nil {
			return Response{}, readErr
		}
		if int64(len(body)) > client.configuration.MaxResponseBytes {
			return Response{}, errors.New("AI provider response is too large")
		}
		text, decodeErr := client.decodeProviderResponse(body)
		if decodeErr != nil {
			return Response{}, decodeErr
		}
		if emitErr := emitStreamCharacters(ctx, done, output, text); emitErr != nil {
			return Response{}, emitErr
		}
		return Response{Text: text}, nil
	}

	return client.readEventStream(ctx, response.Body, output, done)
}

func (client *Client) streamingRequestPayload(request Request) (string, []byte, error) {
	endpoint := client.baseURL + "/chat/completions"
	switch client.configuration.Provider {
	case ProviderOllama:
		endpoint = strings.TrimSuffix(client.baseURL, "/v1") + "/api/chat"
		payload, err := json.Marshal(struct {
			Model    string    `json:"model"`
			Messages []Message `json:"messages"`
			Stream   bool      `json:"stream"`
			Think    bool      `json:"think"`
		}{
			Model:    client.configuration.Model,
			Messages: request.Messages,
			Stream:   true,
			Think:    false,
		})
		return endpoint, payload, err
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
			return "", nil, ErrInvalidConfiguration
		}
		payload, err := json.Marshal(struct {
			Model        string    `json:"model"`
			Instructions string    `json:"instructions,omitempty"`
			Input        []Message `json:"input"`
			Stream       bool      `json:"stream"`
		}{
			Model:        client.configuration.Model,
			Instructions: strings.Join(systemParts, "\n\n"),
			Input:        conversation,
			Stream:       true,
		})
		return endpoint, payload, err
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
			return "", nil, ErrInvalidConfiguration
		}
		payload, err := json.Marshal(struct {
			Model     string    `json:"model"`
			MaxTokens int       `json:"max_tokens"`
			System    string    `json:"system,omitempty"`
			Messages  []Message `json:"messages"`
			Stream    bool      `json:"stream"`
		}{
			Model:     client.configuration.Model,
			MaxTokens: 8192,
			System:    strings.Join(systemParts, "\n\n"),
			Messages:  conversation,
			Stream:    true,
		})
		return endpoint, payload, err
	default:
		payload, err := json.Marshal(struct {
			Model    string    `json:"model"`
			Messages []Message `json:"messages"`
			Stream   bool      `json:"stream"`
		}{
			Model:    client.configuration.Model,
			Messages: request.Messages,
			Stream:   true,
		})
		return endpoint, payload, err
	}
}

func (client *Client) applyProviderHeaders(request *http.Request) {
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream, application/json")
	if client.configuration.Provider != ProviderOllama {
		request.Header.Set("Authorization", "Bearer "+client.configuration.APIKey)
	}
	if client.configuration.Provider == ProviderAnthropic {
		request.Header.Set("x-api-key", client.configuration.APIKey)
		request.Header.Del("Authorization")
		request.Header.Set("anthropic-version", "2023-06-01")
	}
}

func (client *Client) readEventStream(ctx context.Context, body io.Reader, output chan<- StreamChunk, done <-chan struct{}) (Response, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	var responseText strings.Builder
	var streamedBytes int64

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		streamedBytes += int64(len(line) + 1)
		if streamedBytes > client.configuration.MaxResponseBytes {
			return Response{}, errors.New("AI provider response is too large")
		}
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}

		eventJSON := ""
		if strings.HasPrefix(line, "data:") {
			eventJSON = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		} else if strings.HasPrefix(line, "{") {
			// Some local OpenAI-compatible servers stream newline-delimited JSON
			// instead of SSE while keeping the same response schema.
			eventJSON = line
		}
		if eventJSON == "" {
			continue
		}
		if eventJSON == "[DONE]" {
			break
		}

		delta, decodeErr := decodeStreamDelta(client.configuration.Provider, []byte(eventJSON))
		if decodeErr != nil {
			return Response{}, decodeErr
		}
		if delta == "" {
			continue
		}
		responseText.WriteString(delta)
		if emitErr := emitStreamCharacters(ctx, done, output, delta); emitErr != nil {
			return Response{}, emitErr
		}
	}
	if err := scanner.Err(); err != nil {
		return Response{}, err
	}
	if responseText.Len() == 0 {
		return Response{}, errors.New("AI provider response has no output text")
	}
	return Response{Text: responseText.String()}, nil
}

func decodeStreamDelta(provider string, eventJSON []byte) (string, error) {
	if provider == ProviderOpenAICompatible {
		var event struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal(eventJSON, &event); err != nil {
			return "", err
		}
		if event.Type == "response.output_text.delta" {
			return event.Delta, nil
		}
		return "", nil
	}
	if provider == ProviderAnthropic {
		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(eventJSON, &event); err != nil {
			return "", err
		}
		if event.Type == "content_block_delta" && (event.Delta.Type == "" || event.Delta.Type == "text_delta") {
			return event.Delta.Text, nil
		}
		return "", nil
	}

	var compatibleEvent struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
			Message Message `json:"message"`
		} `json:"choices"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Response string `json:"response"`
	}
	if err := json.Unmarshal(eventJSON, &compatibleEvent); err != nil {
		return "", err
	}
	if len(compatibleEvent.Choices) != 0 {
		if compatibleEvent.Choices[0].Delta.Content != "" {
			return compatibleEvent.Choices[0].Delta.Content, nil
		}
		if compatibleEvent.Choices[0].Message.Content != "" {
			return compatibleEvent.Choices[0].Message.Content, nil
		}
	}
	if compatibleEvent.Message.Content != "" {
		return compatibleEvent.Message.Content, nil
	}
	return compatibleEvent.Response, nil
}

func emitStreamCharacters(ctx context.Context, done <-chan struct{}, output chan<- StreamChunk, text string) error {
	for _, character := range text {
		select {
		case <-done:
			return ErrStreamCanceled
		case <-ctx.Done():
			return ctx.Err()
		case output <- StreamChunk{Text: string(character)}:
		}
	}
	return nil
}

func (client *Client) decodeProviderResponse(body []byte) (string, error) {
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
			return "", err
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
			return "", errors.New("AI provider response has no output text")
		}
		return strings.Join(textParts, ""), nil
	}
	if client.configuration.Provider == ProviderAnthropic {
		var anthropicResponse struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(body, &anthropicResponse); err != nil {
			return "", err
		}
		if len(anthropicResponse.Content) == 0 {
			return "", errors.New("AI provider response has no content")
		}
		return anthropicResponse.Content[0].Text, nil
	}
	var compatibleResponse struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &compatibleResponse); err != nil {
		return "", err
	}
	if len(compatibleResponse.Choices) != 0 && compatibleResponse.Choices[0].Message.Content != "" {
		return compatibleResponse.Choices[0].Message.Content, nil
	}
	if compatibleResponse.Message.Content != "" {
		return compatibleResponse.Message.Content, nil
	}
	if compatibleResponse.Response != "" {
		return compatibleResponse.Response, nil
	}
	return "", errors.New("AI provider response has no choices")
}
