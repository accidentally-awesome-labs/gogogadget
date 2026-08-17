---
title: AI / LLM access
description: The llm.Completer seam — any OpenAI-compatible API, forced model, metered tokens.
section: Features
weight: 19
---

LLM access is one interface, `llm.Completer`, over the OpenAI-compatible
`/chat/completions` shape — raw `net/http`, zero new dependencies. One
implementation (`OpenAICompat`) covers every provider that speaks the shape:

| Provider | `LLM_BASE_URL` | Notes |
|---|---|---|
| OpenAI | `https://api.openai.com/v1` (default) | `LLM_API_KEY=sk-…` |
| OpenRouter | `https://openrouter.ai/api/v1` | one key, many models |
| Vercel AI Gateway | `https://ai-gateway.vercel.sh/v1` | gateway key |
| Ollama | `http://localhost:11434/v1` | local dev, any pulled model |
| Groq | `https://api.groq.com/openai/v1` | |

```sh
LLM_API_KEY=            # empty → AI routes 503 not_configured
LLM_BASE_URL=           # optional; default OpenAI
LLM_MODEL=gpt-4o-mini   # required with the key
```

## The seam and the forced model

```go
type Completer interface {
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}
```

`ChatRequest` has **no model field** — the configured model is forced
server-side. That is deliberate cost control: callers (and their API tokens)
can never pick a bigger model. 429/5xx from the provider surface as
`llm.RetryableError` so future job-queue callers can retry; 4xx are plain
errors.

## The metered endpoint

`POST /api/v1/ai/chat` (scope `write`):

```json
{"messages": [{"role": "user", "content": "Summarize our roadmap"}], "max_tokens": 400}
```

Flow: validate (1–20 messages, ≤ 8000 chars each, known roles) → plan-meter
check (`SumUsageByNameSince(org, "ai_tokens", monthStart) + estimate ≥
cap → 402 plan_limit`) → call → respond `{"content", "usage"}` →
`usage.Record(org, "ai_tokens", total, …)` with Polar's structured `_llm`
metadata (`model`, `prompt_tokens`, `completion_tokens`, `total_tokens`) +
`audit.Log("ai.chat")`. Unconfigured → 503 `not_configured`.

Caps live in `billing.Plan.Meters` (free 100k/mo, pro 2.5M/mo, team
unlimited) and render on the billing page as meters. The same usage rows
flush to Polar for real usage-based pricing — see
[Billing](/docs/billing#usage-based-billing).

## Build a chat feature

App-side (HTML) chat is one handler behind `appChain`:

```go
resp, err := s.llm.Chat(ctx, llm.ChatRequest{Messages: msgs})
if err != nil { /* render error */ }
usage.Record(ctx, s.q, org.ClerkOrgID, "ai_tokens",
    int64(resp.PromptTokens+resp.CompletionTokens), "",
    map[string]any{"_llm": map[string]any{"model": resp.Model}})
```

Same rules: check the meter first if the call should be capped, always record
after. A `nil` Completer means unconfigured — render the 503 pattern from
`billing.templ`'s not-configured card.
