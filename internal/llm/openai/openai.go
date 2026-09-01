package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/llm"
)

type Client struct {
	base, apiKey, model string
	http                *http.Client
}

func New(base, apiKey, model string) (*Client, error) {
	if apiKey == "" || model == "" {
		return nil, fmt.Errorf("openai: api key and model are required")
	}
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return &Client{base: strings.TrimRight(base, "/"), apiKey: apiKey, model: model, http: &http.Client{Timeout: 60 * time.Second}}, nil
}

type request struct {
	Model       string        `json:"model"`
	Messages    []llm.Message `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
}
type response struct {
	Model   string `json:"model"`
	Choices []struct {
		Message llm.Message `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (c *Client) Chat(ctx context.Context, r llm.ChatRequest) (llm.ChatResponse, error) {
	if c == nil {
		return llm.ChatResponse{}, llm.ErrNotConfigured
	}
	body, err := json.Marshal(request{Model: c.model, Messages: r.Messages, MaxTokens: r.MaxTokens, Temperature: r.Temperature})
	if err != nil {
		return llm.ChatResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return llm.ChatResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			return llm.ChatResponse{}, &llm.RetryableError{Status: resp.StatusCode, Body: string(raw)}
		}
		return llm.ChatResponse{}, fmt.Errorf("llm: %d: %s", resp.StatusCode, string(raw))
	}
	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return llm.ChatResponse{}, err
	}
	if len(out.Choices) == 0 {
		return llm.ChatResponse{}, fmt.Errorf("llm: response carried no choices")
	}
	return llm.ChatResponse{Content: out.Choices[0].Message.Content, Model: out.Model, PromptTokens: out.Usage.PromptTokens, CompletionTokens: out.Usage.CompletionTokens}, nil
}

var _ llm.Completer = (*Client)(nil)

type Module struct{ Value llm.Completer }
type Deps struct{ Endpoint, APIKey, Model string }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if h != nil {
		if d.Endpoint == "" {
			d.Endpoint = h.Env("LLM_BASE_URL")
		}
		if d.APIKey == "" {
			d.APIKey = h.Env("LLM_API_KEY")
		}
		if d.Model == "" {
			d.Model = h.Env("LLM_MODEL")
		}
	}
	c, err := New(d.Endpoint, d.APIKey, d.Model)
	if err != nil {
		return nil, err
	}
	return &Module{Value: c}, nil
}
func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("openai: completer is required")
	}
	return ctx.Err()
}
