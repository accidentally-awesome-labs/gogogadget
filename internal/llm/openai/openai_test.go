package openai

import (
	"context"
	"encoding/json"
	"github.com/gogogadget/gogogadget/internal/llm"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientUsesForcedModelAndBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("auth=%q", r.Header.Get("Authorization"))
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["model"] != "model" {
			t.Errorf("model=%v", in["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"model","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, "key", "model")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Chat(context.Background(), llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: "hi"}}})
	if err != nil || got.Content != "ok" || got.PromptTokens != 2 || got.CompletionTokens != 3 {
		t.Fatalf("response=%#v err=%v", got, err)
	}
}
func TestNewModuleRequiresCredentials(t *testing.T) {
	if _, err := NewModule(context.Background(), nil, Deps{}); err == nil {
		t.Fatal("missing credentials accepted")
	}
}
