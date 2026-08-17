package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatSuccessParsesUsage(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody chatCompletionsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model": "gpt-4o-mini",
			"choices": [{"message": {"role": "assistant", "content": "Hello back"}}],
			"usage": {"prompt_tokens": 12, "completion_tokens": 7}
		}`))
	}))
	t.Cleanup(srv.Close)

	c := NewOpenAICompat(srv.URL, "sk-test", "gpt-4o-mini")
	resp, err := c.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}}, MaxTokens: 100,
	})
	require.NoError(t, err)
	assert.Equal(t, "Hello back", resp.Content)
	assert.Equal(t, 12, resp.PromptTokens)
	assert.Equal(t, 7, resp.CompletionTokens)
	assert.Equal(t, "Bearer sk-test", gotAuth)
	assert.Equal(t, "/chat/completions", gotPath)
	assert.Equal(t, "gpt-4o-mini", gotBody.Model, "model forced server-side")
}

func TestChat429IsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewOpenAICompat(srv.URL, "sk-test", "m")
	_, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	require.Error(t, err)
	var re *RetryableError
	require.True(t, errors.As(err, &re), "429 maps to RetryableError")
	assert.Equal(t, 429, re.Status)
}

func TestChat400IsPlainError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewOpenAICompat(srv.URL, "sk-test", "m")
	_, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	require.Error(t, err)
	var re *RetryableError
	assert.False(t, errors.As(err, &re), "4xx is not retryable")
}

func TestChatTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)

	c := NewOpenAICompat(srv.URL, "sk-test", "m")
	c.http.Timeout = 50 * time.Millisecond
	_, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	require.Error(t, err)
}
