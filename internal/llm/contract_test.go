package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runCompleterContract is the behavioral contract every Completer
// implementation must satisfy. The factory spins an httptest server running
// the scenario handler and returns a Completer pointed at it. One
// implementation exists today (OpenAICompat); the contract guards future
// implementations (Ollama, gateways, SDK-backed clients) from drifting.
//
// Contract expectations:
//   - success: a 2xx chat-completions body parses into content + token usage.
//   - 429 (and 5xx): surfaces as *RetryableError so job-queue callers retry.
//   - other 4xx: plain error, NOT retryable.
//   - ctx deadline: Chat honors ctx cancellation (returns an error promptly).
func runCompleterContract(t *testing.T, factory func(t *testing.T, handler http.HandlerFunc) Completer) {
	t.Helper()

	t.Run("success parses content and usage", func(t *testing.T) {
		c := factory(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"model": "gpt-4o-mini",
				"choices": [{"message": {"role": "assistant", "content": "Hello back"}}],
				"usage": {"prompt_tokens": 12, "completion_tokens": 7}
			}`))
		})

		resp, err := c.Chat(context.Background(), ChatRequest{
			Messages: []Message{{Role: "user", Content: "hi"}}, MaxTokens: 100,
		})
		require.NoError(t, err)
		assert.Equal(t, "Hello back", resp.Content)
		assert.Equal(t, "gpt-4o-mini", resp.Model)
		assert.Equal(t, 12, resp.PromptTokens)
		assert.Equal(t, 7, resp.CompletionTokens)
	})

	t.Run("429 is retryable", func(t *testing.T) {
		c := factory(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
		})

		_, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
		require.Error(t, err)
		var re *RetryableError
		require.True(t, errors.As(err, &re), "429 maps to RetryableError")
		assert.Equal(t, 429, re.Status)
	})

	t.Run("400 is a plain error", func(t *testing.T) {
		c := factory(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad request"}`))
		})

		_, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
		require.Error(t, err)
		var re *RetryableError
		assert.False(t, errors.As(err, &re), "4xx is not retryable")
	})

	t.Run("ctx deadline is honored", func(t *testing.T) {
		c := factory(t, func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
		})

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err := c.Chat(ctx, ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
		require.Error(t, err, "Chat must abort when ctx deadline passes")
	})
}

// TestOpenAICompatContract runs the seam contract against the OpenAICompat
// implementation.
func TestOpenAICompatContract(t *testing.T) {
	runCompleterContract(t, func(t *testing.T, handler http.HandlerFunc) Completer {
		t.Helper()
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		return NewOpenAICompat(srv.URL, "sk-test", "gpt-4o-mini")
	})
}
