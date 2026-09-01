package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/llm"
	"github.com/gogogadget/gogogadget/internal/usage"
	"github.com/jackc/pgx/v5/pgtype"
)

// AI serves POST /api/v1/ai/chat — the metered LLM endpoint. Unconfigured
// (nil Completer) → 503 not_configured; over the plan meter → 402 plan_limit.
type AI struct {
	Q       *sqlc.Queries
	LLM     llm.Completer
	Catalog billing.PlanCatalog
}

type chatRequest struct {
	Messages  []llm.Message `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

// Chat handles POST /api/v1/ai/chat (scope write).
func (h *AI) Chat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())

	if h.LLM == nil {
		WriteError(w, http.StatusServiceUnavailable, "not_configured",
			"AI is not configured — set LLM_API_KEY + LLM_MODEL. See /docs/ai.")
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", "Request body must be JSON: {\"messages\": [...]}.")
		return
	}
	if len(req.Messages) < 1 || len(req.Messages) > 20 {
		WriteError(w, http.StatusUnprocessableEntity, "validation_error", "messages must have 1–20 entries.")
		return
	}
	est := int64(req.MaxTokens)
	for _, m := range req.Messages {
		if len(m.Content) > 8000 {
			WriteError(w, http.StatusUnprocessableEntity, "validation_error", "message content must be ≤ 8000 characters.")
			return
		}
		if m.Role != "system" && m.Role != "user" && m.Role != "assistant" {
			WriteError(w, http.StatusUnprocessableEntity, "validation_error", "role must be system, user, or assistant.")
			return
		}
		est += int64(len(m.Content)) / 4 // rough prompt-token estimate
	}

	plan := billing.CurrentPlanWithCatalog(ctx, h.Q, org.OrgID, time.Now(), h.Catalog)
	for _, m := range plan.Meters {
		if m.Key != "ai_tokens" || m.LimitPerMonth <= 0 {
			continue
		}
		monthStart := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.UTC)
		used, err := h.Q.SumUsageByNameSince(ctx, sqlc.SumUsageByNameSinceParams{
			OrgID: org.OrgID, Name: "ai_tokens",
			CreatedAt: pgtype.Timestamptz{Time: monthStart, Valid: true},
		})
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "internal_error", "Could not check usage meter.")
			return
		}
		if used+est >= m.LimitPerMonth {
			WriteError(w, http.StatusPaymentRequired, "plan_limit",
				"The "+plan.Name+" plan allows "+strconv.FormatInt(m.LimitPerMonth, 10)+" AI tokens per month. Upgrade for more.")
			return
		}
	}

	resp, err := h.LLM.Chat(ctx, llm.ChatRequest{Messages: req.Messages, MaxTokens: req.MaxTokens})
	if err != nil {
		if errors.Is(err, llm.ErrNotConfigured) {
			WriteError(w, http.StatusServiceUnavailable, "not_configured", "AI is not configured — set LLM_API_KEY + LLM_MODEL. See /docs/ai.")
			return
		}
		WriteError(w, http.StatusBadGateway, "upstream_error", "The AI backend failed: "+err.Error())
		return
	}
	total := int64(resp.PromptTokens + resp.CompletionTokens)
	usage.Record(ctx, h.Q, org.OrgID, "ai_tokens", total, "", map[string]any{
		"_llm": map[string]any{"model": resp.Model, "prompt_tokens": resp.PromptTokens, "completion_tokens": resp.CompletionTokens, "total_tokens": total},
	})
	audit.Log(ctx, h.Q, org.OrgID, "", "ai.chat", map[string]any{"via": "api", "model": resp.Model, "tokens": total})

	WriteJSON(w, http.StatusOK, map[string]any{
		"content": resp.Content,
		"usage":   map[string]any{"prompt_tokens": resp.PromptTokens, "completion_tokens": resp.CompletionTokens},
	})
}
