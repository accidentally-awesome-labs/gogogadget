// Package llm defines the provider-neutral completion contract. Concrete HTTP
// clients live in adapter packages such as internal/llm/openai.
package llm

import (
	"context"
	"errors"
	"fmt"
)

type Message struct{ Role, Content string }
type ChatRequest struct {
	Messages    []Message
	MaxTokens   int
	Temperature float64
}
type ChatResponse struct {
	Content, Model                 string
	PromptTokens, CompletionTokens int
}
type Completer interface {
	Chat(context.Context, ChatRequest) (ChatResponse, error)
}
type RetryableError struct {
	Status int
	Body   string
}

func (e *RetryableError) Error() string { return fmt.Sprintf("llm: %d: %s", e.Status, e.Body) }

var ErrNotConfigured = errors.New("llm not configured")
