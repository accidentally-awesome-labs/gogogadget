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
	ID         int64  `json:"id"`
	ClerkOrgID string `json:"clerk_org_id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func newProjectResponse(p sqlc.Project) projectResponse {
	return projectResponse{
		ID: p.ID, ClerkOrgID: p.ClerkOrgID, Name: p.Name, Status: p.Status,
		CreatedAt: p.CreatedAt.Time.UTC().Format(time.RFC3339),
		UpdatedAt: p.UpdatedAt.Time.UTC().Format(time.RFC3339),
	}
}

// Projects serves /api/v1/projects — the same rules as the HTML transport.
type Projects struct {
	Q *sqlc.Queries
}

// ListProjects handles GET /api/v1/projects (scope read).
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
	projects, err := h.Q.ListProjectsByOrg(r.Context(), sqlc.ListProjectsByOrgParams{
		ClerkOrgID: org.ClerkOrgID, Column2: "",
		Limit: int32(limit), Offset: int32(offset),
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "internal_error", "Could not list projects.")
		return
	}
	out := make([]projectResponse, 0, len(projects))
	for _, p := range projects {
		out = append(out, newProjectResponse(p))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"projects": out, "limit": limit, "offset": offset})
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

	plan := billing.CurrentPlan(ctx, h.Q, org.ClerkOrgID, time.Now())
	if plan.MaxProjects > 0 {
		count, err := h.Q.CountProjectsByOrg(ctx, org.ClerkOrgID)
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

	project, err := h.Q.CreateProject(ctx, sqlc.CreateProjectParams{ClerkOrgID: org.ClerkOrgID, Name: name})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "internal_error", "Could not create project.")
		return
	}
	audit.Log(ctx, h.Q, org.ClerkOrgID, "", "project.created", map[string]any{"id": project.ID, "name": project.Name, "via": "api"})
	WriteJSON(w, http.StatusCreated, newProjectResponse(project))
}
