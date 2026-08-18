package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Wire-format / constructor-config expectations for OpenAICompat that sit
// OUTSIDE the Completer contract (they describe this one impl's HTTP shape):
// Bearer auth header, {base}/chat/completions path, and the model forced
// server-side from constructor config.
func TestOpenAICompatWireFormat(t *testing.T) {
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
	_, err := c.Chat(context.Background(), ChatRequest{
		Messages: []Message{{Role: "user", Content: "hi"}}, MaxTokens: 100,
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-test", gotAuth)
	assert.Equal(t, "/chat/completions", gotPath)
	assert.Equal(t, "gpt-4o-mini", gotBody.Model, "model forced server-side")
}

func TestOpenAICompatDefaultBase(t *testing.T) {
	c := NewOpenAICompat("", "sk-test", "m")
	assert.Equal(t, "https://api.openai.com/v1", c.base, "empty base falls back to OpenAI")
}
