package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCompleter struct {
	resp llm.ChatResponse
	err  error
}

func (f *fakeCompleter) Chat(context.Context, llm.ChatRequest) (llm.ChatResponse, error) {
	return f.resp, f.err
}

func aiChat(t *testing.T, s *Server, token string, body string) (int, string) {
	t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set("Content-Type", "application/json")
	code, _, resBody := serve(t, s, "POST", "/api/v1/ai/chat", []byte(body), h)
	return code, resBody
}

func TestAIChatRequiresToken(t *testing.T) {
	s := integrationServer(t, nil)
	code, _ := aiChat(t, s, "", `{"messages":[{"role":"user","content":"hi"}]}`)
	assert.Equal(t, http.StatusUnauthorized, code)
}

func TestAIChat503WhenUnconfigured(t *testing.T) {
	s := integrationServer(t, nil) // no LLM in Deps
	seedMembership(t, s, "user_ai0", "org_ai0", "org:admin")
	token := createTokenViaUI(t, s, "ai", "write", sessionCookie("user_ai0", "org_ai0", "org:admin"))

	code, body := aiChat(t, s, token, `{"messages":[{"role":"user","content":"hi"}]}`)
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Contains(t, body, "not_configured")
}

func TestAIChatSuccessMeters(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.LLM = &fakeCompleter{resp: llm.ChatResponse{Content: "hello", Model: "gpt-4o-mini", PromptTokens: 40, CompletionTokens: 60}}
	})
	seedMembership(t, s, "user_ai1", "org_ai1", "org:admin")
	token := createTokenViaUI(t, s, "ai", "write", sessionCookie("user_ai1", "org_ai1", "org:admin"))

	code, body := aiChat(t, s, token, `{"messages":[{"role":"user","content":"count"}]}`)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `"hello"`)
	assert.Contains(t, body, `"prompt_tokens":40`)

	// 100 tokens recorded against the org meter.
	var n int64
	require.NoError(t, s.db.QueryRow(t.Context(),
		"SELECT COALESCE(sum(value),0) FROM usage_events WHERE clerk_org_id = 'org_ai1' AND name = 'ai_tokens'").Scan(&n))
	assert.Equal(t, int64(100), n)

	// Meter shows on the billing page.
	code, page := func() (int, string) {
		c, _, b := serve(t, s, "GET", "/app/settings/billing", nil, nil, sessionCookie("user_ai1", "org_ai1", "org:admin"))
		return c, b
	}()
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, page, `data-testid="meter-ai_tokens"`)
	assert.Contains(t, page, "100")
}

func TestAIChat402OverMeter(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.LLM = &fakeCompleter{resp: llm.ChatResponse{Content: "x", Model: "m"}}
	})
	seedMembership(t, s, "user_ai2", "org_ai2", "org:admin")

	// Free plan = 100k/mo. Seed usage at the cap.
	_, err := s.q.InsertUsageEvent(t.Context(), sqlc.InsertUsageEventParams{
		ClerkOrgID: "org_ai2", Name: "ai_tokens", Value: 100_000, Metadata: []byte(`{}`), ExternalID: "",
	})
	require.NoError(t, err)

	token := createTokenViaUI(t, s, "ai", "write", sessionCookie("user_ai2", "org_ai2", "org:admin"))
	code, body := aiChat(t, s, token, `{"messages":[{"role":"user","content":"hi"}]}`)
	assert.Equal(t, http.StatusPaymentRequired, code)
	assert.Contains(t, body, "plan_limit")
}

func TestAIChatValidation(t *testing.T) {
	s := integrationServer(t, func(d *Deps) {
		d.LLM = &fakeCompleter{resp: llm.ChatResponse{Content: "x", Model: "m"}}
	})
	seedMembership(t, s, "user_ai3", "org_ai3", "org:admin")
	token := createTokenViaUI(t, s, "ai", "write", sessionCookie("user_ai3", "org_ai3", "org:admin"))

	code, _ := aiChat(t, s, token, `{"messages":[]}`)
	assert.Equal(t, http.StatusUnprocessableEntity, code)

	code, _ = aiChat(t, s, token, `{"messages":[{"role":"user","content":"`+strings.Repeat("a", 8001)+`"}]}`)
	assert.Equal(t, http.StatusUnprocessableEntity, code)

	code, _ = aiChat(t, s, token, `not json`)
	assert.Equal(t, http.StatusBadRequest, code)
}
