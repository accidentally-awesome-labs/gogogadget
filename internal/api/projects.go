package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/jackc/pgx/v5/pgtype"
)

// ValidateProjectName is the project name rule shared by both transports
// (HTML form and JSON API): required, ≤80 chars after trimming.
func ValidateProjectName(name string) (string, string) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return name, "Name is required."
	case len(name) > 80:
		return name, "Name must be 80 characters or fewer."
	default:
		return name, ""
	}
}

// projectResponse is the public shape of a project. Explicit DTO, not the
// sqlc row: the row carries search_tsv (an internal FTS column) which has no
// business in a public payload, and pinning the fields here means adding a
// column can never silently change the API.
type projectResponse struct {
	ID        int64  `json:"id"`
	OrgID     string `json:"org_id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func newProjectResponse(p sqlc.Project) projectResponse {
	return projectResponse{
		ID: p.ID, OrgID: p.OrgID, Name: p.Name, Status: p.Status,
		CreatedAt: p.CreatedAt.Time.UTC().Format(time.RFC3339),
		UpdatedAt: p.UpdatedAt.Time.UTC().Format(time.RFC3339),
	}
}

type Projects struct {
	Q       *sqlc.Queries
	Catalog billing.PlanCatalog
}

// ListProjects handles GET /api/v1/projects (scope read).
//
// Two pagination modes, one response shape. `cursor` is keyset paging and is
// what clients should follow: it is stable under concurrent writes, because
// it names a row rather than a position — inserting a project while a client
// pages does not shift rows across page boundaries the way offset does, and
// it stays fast at depth (no rows skipped server-side). `offset` is the
// original contract, still honoured; every response carries next_cursor, so
// an offset client can switch to cursors mid-stream without a flag day.
func (h *Projects) ListProjects(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	var (
		projects []sqlc.Project
		err      error
	)
	if raw := r.URL.Query().Get("cursor"); raw != "" || offset == 0 {
		var after cursor
		if raw != "" {
			if after, err = decodeCursor(raw); err != nil {
				WriteError(w, http.StatusBadRequest, "invalid_cursor",
					"The cursor is malformed. Echo back next_cursor verbatim; cursors are opaque.")
				return
			}
		}
		params := sqlc.ListProjectsByOrgCursorParams{OrgID: org.OrgID, Lim: int32(limit) + 1}
		if raw != "" {
			params.CursorCreatedAt = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
			params.CursorID = pgtype.Int8{Int64: after.ID, Valid: true}
		}
		projects, err = h.Q.ListProjectsByOrgCursor(r.Context(), params) // limit+1 probes for a next page
	} else {
		projects, err = h.Q.ListProjectsByOrg(r.Context(), sqlc.ListProjectsByOrgParams{
			OrgID: org.OrgID, Column2: "",
			Limit: int32(limit) + 1, Offset: int32(offset),
		})
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "internal_error", "Could not list projects.")
		return
	}

	// The extra row is a probe, never a result: its presence is the only
	// honest way to say "there is more" without a second COUNT query.
	var next any
	if len(projects) > limit {
		projects = projects[:limit]
		last := projects[len(projects)-1]
		next = encodeCursor(cursor{CreatedAt: last.CreatedAt.Time, ID: last.ID})
	}

	out := make([]projectResponse, 0, len(projects))
	for _, p := range projects {
		out = append(out, newProjectResponse(p))
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"projects": out, "limit": limit, "offset": offset, "next_cursor": next,
	})
}

type createProjectRequest struct {
	Name string `json:"name"`
}

// CreateProject handles POST /api/v1/projects (scope write). Plan limit →
// 402 with code plan_limit.
func (h *Projects) CreateProject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())

	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", "Request body must be JSON: {\"name\": \"…\"}.")
		return
	}
	name, nameErr := ValidateProjectName(req.Name)
	if nameErr != "" {
		WriteError(w, http.StatusUnprocessableEntity, "validation_error", nameErr)
		return
	}

	plan := billing.CurrentPlanWithCatalog(ctx, h.Q, org.OrgID, time.Now(), h.Catalog)
	if plan.MaxProjects > 0 {
		count, err := h.Q.CountProjectsByOrg(ctx, org.OrgID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "internal_error", "Could not check plan limit.")
			return
		}
		if count >= int64(plan.MaxProjects) {
			WriteError(w, http.StatusPaymentRequired, "plan_limit",
				"The "+plan.Name+" plan allows "+strconv.Itoa(plan.MaxProjects)+" projects. Upgrade to create more.")
			return
		}
	}

	project, err := h.Q.CreateProject(ctx, sqlc.CreateProjectParams{OrgID: org.OrgID, Name: name})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "internal_error", "Could not create project.")
		return
	}
	audit.Log(ctx, h.Q, org.OrgID, "", "project.created", map[string]any{"id": project.ID, "name": project.Name, "via": "api"})
	WriteJSON(w, http.StatusCreated, newProjectResponse(project))
}
