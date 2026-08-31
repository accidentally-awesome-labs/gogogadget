// Package llm is the LLM seam: one Completer interface, one implementation
// (any OpenAI-compatible /chat/completions API over plain net/http — OpenAI,
// OpenRouter, Vercel AI Gateway, Ollama, Groq share the shape). Zero new
// dependencies.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Message is one chat turn.
type Message struct {
	Role, Content string
}

// ChatRequest is the completion call. There is NO model field: the configured
// model is forced server-side (cost control — callers cannot pick).
type ChatRequest struct {
	Messages    []Message
	MaxTokens   int
	Temperature float64
}

// ChatResponse carries content + token usage (for metering).
type ChatResponse struct {
	Content, Model                 string
	PromptTokens, CompletionTokens int
}

// Completer is the seam. Nil Completer = unconfigured → routes 503.
type Completer interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// RetryableError wraps 429/5xx so future job-queue use can retry.
type RetryableError struct {
	Status int
	Body   string
}

func (e *RetryableError) Error() string { return fmt.Sprintf("llm: %d: %s", e.Status, e.Body) }

// OpenAICompat talks to {base}/chat/completions with Bearer auth.
type OpenAICompat struct {
	base, apiKey, model string
	http                *http.Client
}

func NewOpenAICompat(base, apiKey, model string) *OpenAICompat {
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return &OpenAICompat{base: base, apiKey: apiKey, model: model, http: &http.Client{Timeout: 60 * time.Second}}
}

type chatCompletionsRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

type chatCompletionsResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (c *OpenAICompat) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	body, err := json.Marshal(chatCompletionsRequest{
		Model: c.model, Messages: req.Messages, MaxTokens: req.MaxTokens, Temperature: req.Temperature,
	})
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			return ChatResponse{}, &RetryableError{Status: resp.StatusCode, Body: string(b)}
		}
		return ChatResponse{}, fmt.Errorf("llm: %d: %s", resp.StatusCode, string(b))
	}
	var out chatCompletionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ChatResponse{}, err
	}
	if len(out.Choices) == 0 {
		return ChatResponse{}, fmt.Errorf("llm: response carried no choices")
	}
	return ChatResponse{
		Content: out.Choices[0].Message.Content, Model: out.Model,
		PromptTokens: out.Usage.PromptTokens, CompletionTokens: out.Usage.CompletionTokens,
	}, nil
}
