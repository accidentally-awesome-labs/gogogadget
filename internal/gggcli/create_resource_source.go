package gggcli

// The source half of `ggg create resource`. Every emitted file is a literal
// body with $Token$ placeholders that resourceSpec.expand substitutes. The
// token delimiter is `$` because it cannot occur in the Go, templ or SQL these
// bodies contain, so a substitution can never swallow real syntax — and the
// bodies stay readable as the language they are, which is what lets a reviewer
// check that the generated slice follows the shipped projects pattern.

import (
	"encoding/json"
	"go/format"
	"strconv"
	"strings"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

const (
	pgxDependencyVersion = "v5.10.0"
	// resourceNameLimit is the shared name rule both transports enforce.
	resourceNameLimit = 80
	// resourcePageSize is the rows-per-page of the generated read surfaces.
	resourcePageSize = 20
)

// formatGo runs the emitted Go through gofmt so the slice a project receives is
// already canonical rather than merely compilable. Unparseable output is
// returned untouched: the compiler's message against the real file is a far
// better report than a formatter's, and TestCreateResourceEmitsParseableGo
// fails on it first anyway.
func formatGo(body string) string {
	formatted, err := format.Source([]byte(body))
	if err != nil {
		return body
	}
	return string(formatted)
}

// queriesSQL renders internal/db/queries/<table>.sql: one query file for the
// table, sqlc-annotated, every UPDATE setting updated_at.
//
// Every parameter is a named sqlc.arg, and the tenant parameter is called
// `tenant` rather than `org_id`/`user_id`, so the generated Params field is the
// same identifier for every scope. The handlers therefore have one shape
// instead of three.
func (r resourceSpec) queriesSQL() string {
	tenantFilter := "WHERE $tcol$ = sqlc.arg(tenant)\n  AND (sqlc.arg(query)::text = '' OR name ILIKE '%' || sqlc.arg(query) || '%')\n"
	openFilter := "WHERE (sqlc.arg(query)::text = '' OR name ILIKE '%' || sqlc.arg(query) || '%')\n"
	listFilter, countFilter := openFilter, openFilter
	if r.tenantColumn != "" {
		listFilter, countFilter = tenantFilter, tenantFilter
	}

	var b strings.Builder
	b.WriteString(`-- name: List$Exps$ :many
-- The $slug$ resource owns this file: one query file per table, and every
-- UPDATE below sets updated_at = now().
--
-- Newest first, with an optional case-insensitive name filter. An empty filter
-- matches every row, so one query serves the plain list and the search box
-- instead of two that can drift apart. The id tiebreak makes the order total.
SELECT * FROM $table$
`)
	b.WriteString(listFilter)
	b.WriteString(`ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: Count$Exps$ :one
SELECT count(*) FROM $table$
`)
	b.WriteString(strings.TrimSuffix(countFilter, "\n") + ";\n")

	if r.adminUsesAllRows() {
		b.WriteString("\n-- name: ListAll$Exps$ :many\n" + `-- The admin read surface: every tenant's rows, same filter and order. A staff
-- surface that showed only the operator's own tenant would not be an admin
-- surface at all.
SELECT * FROM $table$
` + openFilter + `ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountAll$Exps$ :one
SELECT count(*) FROM $table$
` + strings.TrimSuffix(openFilter, "\n") + ";\n")
	}

	b.WriteString("\n-- name: Get$Exp$ByID :one\nSELECT * FROM $table$ WHERE id = sqlc.arg(id)")
	if r.tenantColumn != "" {
		b.WriteString(" AND $tcol$ = sqlc.arg(tenant)")
	}
	b.WriteString(";\n")

	b.WriteString("\n-- name: Create$Exp$ :one\n")
	if r.tenantColumn != "" {
		b.WriteString("INSERT INTO $table$ ($tcol$, name)\nVALUES (sqlc.arg(tenant), sqlc.arg(name))\nRETURNING *;\n")
	} else {
		b.WriteString("INSERT INTO $table$ (name)\nVALUES (sqlc.arg(name))\nRETURNING *;\n")
	}

	b.WriteString("\n-- name: Update$Exp$ :one\nUPDATE $table$ SET name = sqlc.arg(name), updated_at = now()\nWHERE id = sqlc.arg(id)")
	if r.tenantColumn != "" {
		b.WriteString(" AND $tcol$ = sqlc.arg(tenant)")
	}
	b.WriteString("\nRETURNING *;\n")

	// :execrows, not :exec — the row count is how the handler tells "deleted"
	// from "never existed" without a second round trip, and a delete that
	// matched nothing must be a 404 rather than a cheerful 200.
	b.WriteString("\n-- name: Delete$Exp$ :execrows\nDELETE FROM $table$ WHERE id = sqlc.arg(id)")
	if r.tenantColumn != "" {
		b.WriteString(" AND $tcol$ = sqlc.arg(tenant)")
	}
	b.WriteString(";\n")
	return r.expand(b.String())
}

// migrationSQL renders the immutable, forward-only migration. The table carries
// exactly the columns the emitted queries and handlers use, and a tenant-scoped
// table carries the ON DELETE CASCADE foreign key that makes its declared
// cascade behaviour true of the schema rather than only of the manifest.
func (r resourceSpec) migrationSQL() string {
	var b strings.Builder
	b.WriteString("-- +goose Up\nCREATE TABLE $table$ (\n  id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,\n")
	switch r.scope {
	case "org":
		b.WriteString("  org_id     TEXT NOT NULL REFERENCES orgs(org_id) ON DELETE CASCADE,\n")
	case "user":
		b.WriteString("  user_id    TEXT NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,\n")
	}
	b.WriteString(`  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
`)
	if r.tenantColumn != "" {
		b.WriteString("CREATE INDEX $table$_tenant_idx ON $table$ ($tcol$, created_at DESC);\n")
	} else {
		b.WriteString("CREATE INDEX $table$_created_idx ON $table$ (created_at DESC);\n")
	}
	b.WriteString("\n-- +goose Down\n-- forward-only migration\n")
	return r.expand(b.String())
}

// transportGo renders internal/web/workflow_<snake>.go: the app read page, the
// four mutations, the optional admin read surface, the optional JSON transport
// and the optional search wiring — all methods on *web.Server, all declared in
// the manifest so the generated mux registers them.
func (r resourceSpec) transportGo() string {
	var b strings.Builder
	b.WriteString("package web\n\n")
	b.WriteString(r.transportImports())
	if !r.noUI {
		b.WriteString("\n// $low$PageSize is the rows-per-page of the read surfaces.\nconst $low$PageSize = " +
			strconv.Itoa(resourcePageSize) + "\n")
	}
	if r.hasHandlers() {
		b.WriteString(r.transportValidator())
	}
	if !r.noUI {
		b.WriteString(r.transportReadPage())
		b.WriteString(r.transportFormError())
		b.WriteString(r.transportCreate())
		b.WriteString(r.transportUpdate())
		b.WriteString(r.transportDelete())
	}
	if r.admin {
		b.WriteString(r.transportAdmin())
	}
	if r.api {
		b.WriteString(r.transportAPI())
	}
	if r.search {
		b.WriteString(r.transportSearch())
	}
	return formatGo(r.expand(b.String()))
}

// transportImports emits exactly the imports the selected surfaces use. An
// import the body never touches is a compile error, so this is derived from the
// same flags the bodies are.
func (r resourceSpec) transportImports() string {
	var std, project []string
	if r.search {
		std = append(std, "context")
	}
	if r.api {
		std = append(std, "encoding/json")
	}
	if r.hasHandlers() {
		std = append(std, "errors")
	}
	if !r.noUI {
		std = append(std, "math")
	}
	if r.hasHandlers() {
		std = append(std, "net/http", "strconv", "strings")
	}
	if r.api {
		std = append(std, "time")
	}
	if r.api {
		project = append(project, "github.com/gogogadget/gogogadget/internal/api")
	}
	if r.hasHandlers() {
		project = append(project,
			"github.com/gogogadget/gogogadget/internal/db/sqlc",
			"github.com/gogogadget/gogogadget/internal/identity")
	}
	if r.search {
		project = append(project, "github.com/gogogadget/gogogadget/internal/search")
	}
	if !r.noUI {
		project = append(project, "github.com/gogogadget/gogogadget/internal/web/templates")
	}
	if r.hasHandlers() {
		project = append(project, "github.com/jackc/pgx/v5")
	}
	var b strings.Builder
	b.WriteString("import (\n")
	for _, path := range std {
		b.WriteString("\t\"" + path + "\"\n")
	}
	if len(project) != 0 {
		b.WriteString("\n")
		for _, path := range project {
			b.WriteString("\t\"" + path + "\"\n")
		}
	}
	b.WriteString(")\n")
	return b.String()
}

func (r resourceSpec) transportValidator() string {
	limit := strconv.Itoa(resourceNameLimit)
	return `
// $low$FormInput is the HTML form body.
// The JSON transport has its own request type; both run the same validator, so
// the two transports cannot disagree about what a valid name is.
type $low$FormInput struct {
	Name string ` + "`form:\"name\"`" + `
}

// validate$Exp$Name is that shared rule: required,
// ` + limit + ` characters or fewer after trimming. It returns the cleaned value and a
// message, empty when valid.
func validate$Exp$Name(name string) (string, string) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return name, "Name is required."
	case len(name) > ` + limit + `:
		return name, "Name must be ` + limit + ` characters or fewer."
	default:
		return name, ""
	}
}
`
}

// identityDeclarations emits the identity lookups a handler body reads. Only
// those: an unused local is a compile error, so this cannot be generous.
func (r resourceSpec) identityDeclarations(audits, tenanted bool) string {
	var b strings.Builder
	if audits || (tenanted && r.scope == "org") {
		b.WriteString("\torg := identity.OrgFrom(ctx)\n")
	}
	if audits || (tenanted && r.scope == "user") {
		b.WriteString("\tuser := identity.UserFrom(ctx)\n")
	}
	return b.String()
}

// tenantValue is the Go expression for the tenant column's value on a browser
// route, where both the organization and the user are in the context.
func (r resourceSpec) tenantValue() string {
	switch r.scope {
	case "org":
		return "org.OrgID"
	case "user":
		return "user.UserID"
	default:
		return ""
	}
}

// sqlc emits a Params struct only for a query with more than one parameter, so
// a call whose tenant predicate is absent passes a bare value where a
// tenant-filtered one passes a struct. These four builders are the only place
// that distinction lives.
func (r resourceSpec) listArgs(prefix, limit, offset, tenant string) string {
	fields := "Query: query, Lim: " + limit + ", Off: " + offset
	if tenant != "" {
		fields = "Tenant: " + tenant + ", " + fields
	}
	return "sqlc." + prefix + r.exports + "Params{" + fields + "}"
}

func (r resourceSpec) countArgs(prefix, tenant string) string {
	if tenant == "" {
		return "query"
	}
	return "sqlc." + prefix + r.exports + "Params{Tenant: " + tenant + ", Query: query}"
}

func (r resourceSpec) getArgs(tenant string) string {
	if tenant == "" {
		return "id"
	}
	return "sqlc.Get" + r.exported + "ByIDParams{ID: id, Tenant: " + tenant + "}"
}

func (r resourceSpec) createArgs(tenant string) string {
	if tenant == "" {
		return "name"
	}
	return "sqlc.Create" + r.exported + "Params{Tenant: " + tenant + ", Name: name}"
}

func (r resourceSpec) updateArgs(tenant string) string {
	if tenant == "" {
		return "sqlc.Update" + r.exported + "Params{ID: id, Name: name}"
	}
	return "sqlc.Update" + r.exported + "Params{ID: id, Tenant: " + tenant + ", Name: name}"
}

func (r resourceSpec) deleteArgs(tenant string) string {
	if tenant == "" {
		return "id"
	}
	return "sqlc.Delete" + r.exported + "Params{ID: id, Tenant: " + tenant + "}"
}

// listBody is the shared read body: count, page, rows, view items. The prefixes
// select the tenant-filtered queries or the admin-wide ones.
func (r resourceSpec) listBody(countPrefix, listPrefix, tenant string) string {
	return `	query := strings.TrimSpace(r.URL.Query().Get("q"))
	pageNumber, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if pageNumber < 1 {
		pageNumber = 1
	}
	total, err := s.q.` + countPrefix + `$Exps$(ctx, ` + r.countArgs(countPrefix, tenant) + `)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	rows, err := s.q.` + listPrefix + `$Exps$(ctx, ` +
		r.listArgs(listPrefix, "$low$PageSize", "int32((pageNumber-1)*$low$PageSize)", tenant) + `)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	// An explicit view row rather than the query row: the template package then
	// depends on nothing generated, and adding a column cannot silently change
	// what the table renders.
	items := make([]templates.$Exp$Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, templates.$Exp$Item{ID: row.ID, Name: row.Name, CreatedAt: row.CreatedAt.Time})
	}
`
}

func (r resourceSpec) transportReadPage() string {
	tenant := r.tenantValue()
	return `
// GET $route$
//
// The read surface: search, pagination, and the create/edit form on one page.
// ?edit=<id> loads a row into that form, so editing needs neither a second
// route nor a second template.
func (s *Server) handle$Exps$(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
` + r.identityDeclarations(false, true) + r.listBody("Count", "List", tenant) + `	data := templates.$Exps$ListData{
		Items: items, Query: query, Page: pageNumber,
		TotalPages: max(int(math.Ceil(float64(total)/$low$PageSize)), 1),
		BaseURL:    "$route$",
	}
	if raw := r.URL.Query().Get("edit"); raw != "" {
		id, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			s.handleNotFound(w, r)
			return
		}
		// A row outside the caller's tenant is a 404, never a 403: a 403 would
		// confirm that the id exists.
		row, getErr := s.q.Get$Exp$ByID(ctx, ` + r.getArgs(tenant) + `)
		if errors.Is(getErr, pgx.ErrNoRows) {
			s.handleNotFound(w, r)
			return
		}
		if getErr != nil {
			s.renderError(w, r, getErr.Error())
			return
		}
		data.Form = templates.$Exp$FormData{ID: row.ID, Name: row.Name}
	}
	pageData := Page{Title: "$HumanPlural$", Layout: templates.LayoutApp}
	if wantsFragment(r) {
		s.Render(w, r, pageData, templates.$Exps$Table(data))
		return
	}
	s.Render(w, r, pageData, templates.$Exps$Page(data))
}
`
}

func (r resourceSpec) transportFormError() string {
	return `
// render$Exp$FormError re-renders the form with 422:
// the fragment for htmx, which swaps it into #$slug$-form, and the standalone
// form page otherwise.
func (s *Server) render$Exp$FormError(w http.ResponseWriter, r *http.Request, form templates.$Exp$FormData) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	pageData := Page{Title: "$HumanPlural$", Layout: templates.LayoutApp}
	if wantsFragment(r) {
		s.Render(w, r, pageData, templates.$Exp$Form(form))
		return
	}
	s.Render(w, r, pageData, templates.$Exp$FormPage(form))
}
`
}

func (r resourceSpec) transportCreate() string {
	return `
// POST $route$
func (s *Server) handle$Exp$Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
` + r.identityDeclarations(true, true) + `	var input $low$FormInput
	if err := decodeForm(r, &input); err != nil {
		s.render$Exp$FormError(w, r, templates.$Exp$FormData{})
		return
	}
	name, nameErr := validate$Exp$Name(input.Name)
	if nameErr != "" {
		s.render$Exp$FormError(w, r, templates.$Exp$FormData{Name: name, NameErr: nameErr})
		return
	}
	row, err := s.q.Create$Exp$(ctx, ` + r.createArgs(r.tenantValue()) + `)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.logAudit(ctx, org.OrgID, user.UserID, "$snake$.created", map[string]any{"id": row.ID, "name": row.Name})
` + r.indexCall("row.ID", "row.Name") + `	Toast(w, "success", "$HumanSingular$ created")
	Navigate(w, r, "$route$")
}
`
}

func (r resourceSpec) transportUpdate() string {
	return `
// POST $route$/{id}
func (s *Server) handle$Exp$Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
` + r.identityDeclarations(true, true) + `	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.handleNotFound(w, r)
		return
	}
	var input $low$FormInput
	if decodeErr := decodeForm(r, &input); decodeErr != nil {
		s.render$Exp$FormError(w, r, templates.$Exp$FormData{ID: id})
		return
	}
	name, nameErr := validate$Exp$Name(input.Name)
	if nameErr != "" {
		s.render$Exp$FormError(w, r, templates.$Exp$FormData{ID: id, Name: name, NameErr: nameErr})
		return
	}
	// The tenant predicate is in the UPDATE, so a cross-tenant id matches no
	// row and arrives here as ErrNoRows: one round trip, no existence leak.
	row, err := s.q.Update$Exp$(ctx, ` + r.updateArgs(r.tenantValue()) + `)
	if errors.Is(err, pgx.ErrNoRows) {
		s.handleNotFound(w, r)
		return
	}
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.logAudit(ctx, org.OrgID, user.UserID, "$snake$.updated", map[string]any{"id": row.ID, "name": row.Name})
` + r.indexCall("row.ID", "row.Name") + `	Toast(w, "success", "$HumanSingular$ updated")
	Navigate(w, r, "$route$")
}
`
}

func (r resourceSpec) transportDelete() string {
	return `
// DELETE $route$/{id}
//
// Row swap: 200 with an empty body, and htmx removes the tr.
func (s *Server) handle$Exp$Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
` + r.identityDeclarations(true, true) + `	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.handleNotFound(w, r)
		return
	}
	affected, err := s.q.Delete$Exp$(ctx, ` + r.deleteArgs(r.tenantValue()) + `)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	if affected == 0 {
		s.handleNotFound(w, r)
		return
	}
	s.logAudit(ctx, org.OrgID, user.UserID, "$snake$.deleted", map[string]any{"id": id})
` + r.deleteIndexCall("id") + `	Toast(w, "success", "$HumanSingular$ deleted")
	w.WriteHeader(http.StatusOK)
}
`
}

func (r resourceSpec) transportAdmin() string {
	countPrefix, listPrefix, tenant := "Count", "List", ""
	if r.adminUsesAllRows() {
		countPrefix, listPrefix = "CountAll", "ListAll"
	}
	return `
// GET /admin/$plural$
//
// The staff read surface. It renders the same table with ReadOnly set, so it
// cannot become a second write path by accident.
func (s *Server) handleAdmin$Exps$(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
` + r.listBody(countPrefix, listPrefix, tenant) + `	data := templates.$Exps$ListData{
		Items: items, Query: query, Page: pageNumber,
		TotalPages: max(int(math.Ceil(float64(total)/$low$PageSize)), 1),
		BaseURL:    "/admin/$plural$", ReadOnly: true,
	}
	pageData := Page{Title: "$HumanPlural$", Layout: templates.LayoutAdmin}
	if wantsFragment(r) {
		s.Render(w, r, pageData, templates.$Exps$Table(data))
		return
	}
	s.Render(w, r, pageData, templates.Admin$Exps$Page(data))
}
`
}

// indexCall emits the search upsert, or nothing when --search was not asked
// for. The tenant is always the acting organization: the search index is
// organization-scoped, so that is the only tenant it can accept.
func (r resourceSpec) indexCall(id, name string) string {
	if !r.search {
		return ""
	}
	return "\ts.index$Exp$(ctx, org.OrgID, strconv.FormatInt(" + id + ", 10), " + name + ")\n"
}

func (r resourceSpec) deleteIndexCall(id string) string {
	if !r.search {
		return ""
	}
	return "\ts.delete$Exp$Index(ctx, org.OrgID, strconv.FormatInt(" + id + ", 10))\n"
}

func (r resourceSpec) transportSearch() string {
	return `
// index$Exp$ and delete$Exp$Index keep the search document
// for one row in step with the table. Both are fire-and-forget: a search hiccup
// must never fail the mutation that already succeeded.
func (s *Server) index$Exp$(ctx context.Context, tenantID, id, name string) {
	if s.searchIndex == nil {
		return
	}
	_ = s.searchIndex.Upsert(ctx, search.Document{TenantID: tenantID, Collection: "$table$", ID: id, Text: name})
}

func (s *Server) delete$Exp$Index(ctx context.Context, tenantID, id string) {
	if s.searchIndex != nil {
		_ = s.searchIndex.Delete(ctx, tenantID, "$table$", id)
	}
}
`
}

// transportAPI emits the /api/v1 JSON transport: the same authorization rules
// as the UI, Bearer-token identity, and the api package's error envelope.
func (r resourceSpec) transportAPI() string {
	// An API token carries an organization and no user, so the tenant is always
	// the token's organization rather than the UI's scope-dependent value.
	tenant := ""
	if r.tenantColumn != "" {
		tenant = "org.OrgID"
	}
	return `
// $low$Response is the public shape of one row.
// An explicit DTO, not the query row: pinning the fields here means adding a
// column can never silently change the API.
type $low$Response struct {
	ID        int64  ` + "`json:\"id\"`" + `
	Name      string ` + "`json:\"name\"`" + `
	CreatedAt string ` + "`json:\"created_at\"`" + `
	UpdatedAt string ` + "`json:\"updated_at\"`" + `
}

func new$Exp$Response(id int64, name string, createdAt, updatedAt time.Time) $low$Response {
	return $low$Response{
		ID: id, Name: name,
		CreatedAt: createdAt.UTC().Format(time.RFC3339),
		UpdatedAt: updatedAt.UTC().Format(time.RFC3339),
	}
}

type $low$APIRequest struct {
	Name string ` + "`json:\"name\"`" + `
}

// GET /api/v1/$plural$ (scope read).
func (s *Server) handleAPIList$Exps$(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
` + r.apiOrgDeclaration() + `	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	// The JSON transport does not expose the UI's free-text filter, so the
	// shared query runs with an empty one, which matches every row.
	query := ""
	rows, err := s.q.List$Exps$(ctx, ` + r.listArgs("List", "int32(limit)", "int32(offset)", tenant) + `)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "internal_error", "Could not list $humanPlural$.")
		return
	}
	out := make([]$low$Response, 0, len(rows))
	for _, row := range rows {
		out = append(out, new$Exp$Response(row.ID, row.Name, row.CreatedAt.Time, row.UpdatedAt.Time))
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "limit": limit, "offset": offset})
}

// POST /api/v1/$plural$ (scope write).
func (s *Server) handleAPICreate$Exp$(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(ctx)
	var request $low$APIRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json", ` + "`" + `Request body must be JSON: {"name": "…"}.` + "`" + `)
		return
	}
	name, nameErr := validate$Exp$Name(request.Name)
	if nameErr != "" {
		api.WriteError(w, http.StatusUnprocessableEntity, "validation_error", nameErr)
		return
	}
	row, err := s.q.Create$Exp$(ctx, ` + r.createArgs(tenant) + `)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "internal_error", "Could not create the $humanSingular$.")
		return
	}
	// The token carries an organization and no user, so the audit actor is the
	// organization with an empty user rather than a guess.
	s.logAudit(ctx, org.OrgID, "", "$snake$.created", map[string]any{"id": row.ID, "name": row.Name, "via": "api"})
` + r.apiIndexCall("row.ID", "row.Name") + `	api.WriteJSON(w, http.StatusCreated, new$Exp$Response(row.ID, row.Name, row.CreatedAt.Time, row.UpdatedAt.Time))
}

// PATCH /api/v1/$plural$/{id} (scope write).
func (s *Server) handleAPIUpdate$Exp$(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(ctx)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "not_found", "No such $humanSingular$.")
		return
	}
	var request $low$APIRequest
	if decodeErr := json.NewDecoder(r.Body).Decode(&request); decodeErr != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_json", ` + "`" + `Request body must be JSON: {"name": "…"}.` + "`" + `)
		return
	}
	name, nameErr := validate$Exp$Name(request.Name)
	if nameErr != "" {
		api.WriteError(w, http.StatusUnprocessableEntity, "validation_error", nameErr)
		return
	}
	row, err := s.q.Update$Exp$(ctx, ` + r.updateArgs(tenant) + `)
	if errors.Is(err, pgx.ErrNoRows) {
		api.WriteError(w, http.StatusNotFound, "not_found", "No such $humanSingular$.")
		return
	}
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "internal_error", "Could not update the $humanSingular$.")
		return
	}
	s.logAudit(ctx, org.OrgID, "", "$snake$.updated", map[string]any{"id": row.ID, "name": row.Name, "via": "api"})
` + r.apiIndexCall("row.ID", "row.Name") + `	api.WriteJSON(w, http.StatusOK, new$Exp$Response(row.ID, row.Name, row.CreatedAt.Time, row.UpdatedAt.Time))
}

// DELETE /api/v1/$plural$/{id} (scope write).
func (s *Server) handleAPIDelete$Exp$(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(ctx)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "not_found", "No such $humanSingular$.")
		return
	}
	affected, err := s.q.Delete$Exp$(ctx, ` + r.deleteArgs(tenant) + `)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "internal_error", "Could not delete the $humanSingular$.")
		return
	}
	if affected == 0 {
		api.WriteError(w, http.StatusNotFound, "not_found", "No such $humanSingular$.")
		return
	}
	s.logAudit(ctx, org.OrgID, "", "$snake$.deleted", map[string]any{"id": id, "via": "api"})
` + r.apiDeleteIndexCall("id") + `	w.WriteHeader(http.StatusNoContent)
}
`
}

// apiOrgDeclaration declares the organization in the list handler only when
// that body reads it. It is the tenant filter and nothing else there, so a
// platform-scoped table declares nothing.
func (r resourceSpec) apiOrgDeclaration() string {
	if r.tenantColumn == "" {
		return ""
	}
	return "\torg := identity.OrgFrom(ctx)\n"
}

func (r resourceSpec) apiIndexCall(id, name string) string {
	return r.indexCall(id, name)
}

func (r resourceSpec) apiDeleteIndexCall(id string) string {
	return r.deleteIndexCall(id)
}

// transportTestGo covers the one piece of the slice that is pure logic. Route
// behaviour needs a database and belongs in an integration test, which is why
// this file asserts the shared name rule and nothing it cannot honestly reach.
func (r resourceSpec) transportTestGo() string {
	return formatGo(r.expand(`package web

import (
	"strings"
	"testing"
)

func Test$Exp$NameValidation(t *testing.T) {
	if name, message := validate$Exp$Name("  Launch  "); name != "Launch" || message != "" {
		t.Fatalf("validate$Exp$Name(padded) = (%q, %q), want (\"Launch\", \"\")", name, message)
	}
	if _, message := validate$Exp$Name("   "); message == "" {
		t.Fatal("validate$Exp$Name accepted a blank name")
	}
	if _, message := validate$Exp$Name(strings.Repeat("x", ` + strconv.Itoa(resourceNameLimit+1) + `)); message == "" {
		t.Fatal("validate$Exp$Name accepted a name over the length limit")
	}
}
`))
}

// templatesTempl renders internal/web/templates/<slug>.templ: the table card,
// both empty states, the shared form and the delete confirmation. Only ui
// components, every string through i18n, a data-testid on every element a test
// asserts on.
func (r resourceSpec) templatesTempl() string {
	body := templResourceHead + templResourceAppPage
	if r.admin {
		body += templResourceAdminPage
	}
	body += templResourceTable + templResourceForm
	return r.expand(body)
}

const templResourceHead = `package templates

import (
	"context"
	"fmt"
	"time"

	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
)

// $Exp$Item is the view row for $table$.
// The handler maps query rows onto it, so this package depends on nothing
// generated and adding a column cannot silently change what the table renders.
type $Exp$Item struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}

// $Exps$ListData is the read surface state.
// One page of rows, the search term, the pager position, the base URL every
// control links back to, and the form that shares the page. ReadOnly drops the
// row actions, which is what makes the admin surface a read surface rather than
// a second write path.
type $Exps$ListData struct {
	Items      []$Exp$Item
	Query      string
	Page       int
	TotalPages int
	BaseURL    string
	ReadOnly   bool
	Form       $Exp$FormData
}

// $Exp$FormData is the create/edit form state. ID 0 is a create.
type $Exp$FormData struct {
	ID      int64
	Name    string
	NameErr string
}
`

const templResourceAppPage = `
templ $Exps$Page(d $Exps$ListData) {
	@ui.PageHeader(ui.PageHeaderOpts{Title: i18n.T(ctx, "$slug$.title")})
	@$Exp$Form(d.Form)
	// The search box stays OUTSIDE the swap target: it morphs the table
	// container on every keystroke, and a control inside the box it replaces is
	// re-created mid-word, which loses the caret.
	@ui.TableToolbar(ui.TableToolbarOpts{Label: i18n.T(ctx, "$slug$.toolbar_label")}) {
		@$lows$Search(d)
	}
	<div id="$slug$-table-container">
		@$Exps$Table(d)
	</div>
}
`

const templResourceAdminPage = `
// Admin$Exps$Page is the staff read surface.
// Same table, ReadOnly set by the handler, so the rows carry no write controls.
templ Admin$Exps$Page(d $Exps$ListData) {
	@ui.PageHeader(ui.PageHeaderOpts{Title: i18n.T(ctx, "$slug$.admin_title")})
	@ui.TableToolbar(ui.TableToolbarOpts{Label: i18n.T(ctx, "$slug$.toolbar_label")}) {
		@$lows$Search(d)
	}
	<div id="$slug$-table-container">
		@$Exps$Table(d)
	</div>
}
`

const templResourceTable = `
templ $lows$Search(d $Exps$ListData) {
	@ui.SearchInput(ui.SearchInputOpts{
		Name: "q", Value: d.Query,
		Placeholder: i18n.T(ctx, "$slug$.search_placeholder"),
		AriaLabel:   i18n.T(ctx, "$slug$.search_label"),
		GetURL:      d.BaseURL, Target: "#$slug$-table-container",
		IndicatorID: "$slug$-search-indicator",
	})
}

// $Exps$Table is the search and pager fragment.
// The CONTENTS of the table container, never the box itself: both controls swap
// innerMorph, so a fragment repeating its own wrapper would leave a second
// element carrying the same id.
templ $Exps$Table(d $Exps$ListData) {
	@ui.DataTable($lows$Table(ctx, d)) {
		for _, item := range d.Items {
			<tr id={ fmt.Sprintf("$slug$-%d", item.ID) } data-testid="$slug$-row">
				<td class="px-4 py-3 font-medium">{ item.Name }</td>
				<td class="px-4 py-3 text-fg-muted">{ item.CreatedAt.Format("Jan 2, 2006") }</td>
				if !d.ReadOnly {
					<td class="px-4 py-3">
						<div class="card-actions">
							@ui.ButtonLink(ui.ButtonLinkOpts{
								Label: i18n.T(ctx, "$slug$.edit"),
								Href:  fmt.Sprintf("%s?edit=%d", d.BaseURL, item.ID),
								Size:  ui.SizeXS,
							})
							// The request rides on the dialog's confirm control,
							// so the delete contract (closest tr, outerHTML,
							// 200-empty) is unchanged while the prompt stays
							// translatable and assertable. hx-confirm calls
							// window.confirm and can be neither.
							@ui.ConfirmAction(ui.ConfirmActionOpts{
								ID: fmt.Sprintf("$slug$-delete-%d", item.ID),
								// The row's subject is in the trigger's name:
								// forty controls all called "Delete" are
								// indistinguishable to a screen reader.
								TriggerLabel: i18n.T(ctx, "$slug$.delete_row", item.Name),
								TriggerIcon:  ui.IconDelete,
								Title:        i18n.T(ctx, "$slug$.delete_title"),
								Message:      i18n.T(ctx, "$slug$.delete_confirm"),
								ConfirmLabel: i18n.T(ctx, "$slug$.delete_action"),
								CancelLabel:  i18n.T(ctx, "$slug$.cancel"),
								Kind:         ui.KindDanger,
								HX: ui.HX{
									Delete: fmt.Sprintf("%s/%d", d.BaseURL, item.ID),
									Target: "closest tr", Swap: "outerHTML",
								},
							})
						</div>
					</td>
				}
			</tr>
		}
	}
}

// $lows$Table assembles the list and its slots.
// Empty and Pagination are passed in every state: a caller that renders the
// table only when rows exist takes the pager away with the rows.
func $lows$Table(ctx context.Context, d $Exps$ListData) ui.DataTableOpts {
	columns := []ui.Column{
		{Key: "name", Label: i18n.T(ctx, "$slug$.col_name")},
		{Key: "created", Label: i18n.T(ctx, "$slug$.col_created")},
	}
	if !d.ReadOnly {
		columns = append(columns, ui.Column{Key: "actions", Label: i18n.T(ctx, "$slug$.col_actions"), Align: ui.AlignEnd})
	}
	return ui.DataTableOpts{
		Caption:       i18n.T(ctx, "$slug$.caption"),
		HiddenCaption: true,
		Columns:       columns,
		RowCount:      len(d.Items),
		Empty:         $lows$Empty(d),
		Pagination:    $lows$Pager(d),
		Attrs:         ui.Attrs{ID: "$slug$-table", TestID: "$slug$-table"},
	}
}

// $lows$Empty separates "nothing exists yet" from
// "your search matched nothing". Offering "add your first one" to somebody
// whose query missed is the wrong answer to the wrong question.
templ $lows$Empty(d $Exps$ListData) {
	if d.Query != "" {
		@ui.EmptyState(ui.EmptyStateOpts{
			Level: 2, Variant: ui.EmptyInline, Filtered: true,
			Title:      i18n.T(ctx, "$slug$.filtered_empty_title"),
			Body:       i18n.T(ctx, "$slug$.filtered_empty_body", d.Query),
			ClearURL:   d.BaseURL,
			ClearLabel: i18n.T(ctx, "$slug$.clear_search"),
			Target:     "#$slug$-table-container",
		})
	} else {
		@ui.EmptyState(ui.EmptyStateOpts{
			Level: 2, Variant: ui.EmptyInline,
			Title: i18n.T(ctx, "$slug$.empty_title"),
			Body:  i18n.T(ctx, "$slug$.empty_body"),
		})
	}
}

templ $lows$Pager(d $Exps$ListData) {
	@ui.Pagination(ui.PaginationOpts{
		Page: d.Page, TotalPages: d.TotalPages,
		BaseURL: d.BaseURL + "?q=" + d.Query, Target: "#$slug$-table-container",
		Numbered: true,
		Labels:   $lows$PagerLabels(ctx),
	})
}

// $lows$PagerLabels is the shared pager vocabulary plus
// the per-number link name. Without it a screen reader hears only the digit,
// which in a row of digits says nothing about what the control does.
func $lows$PagerLabels(ctx context.Context) ui.PagerLabels {
	labels := pagerLabels(ctx)
	labels.Page = func(page int) string { return i18n.T(ctx, "$slug$.pagination_page", page) }
	return labels
}
`

const templResourceForm = `
templ $Exp$Form(d $Exp$FormData) {
	<div id="$slug$-form">
		if d.NameErr != "" {
			@ui.Notice(ui.NoticeOpts{Kind: ui.KindDanger, Live: ui.LiveAssertive, Attrs: ui.Attrs{Class: "mb-4"}}) {
				{ i18n.T(ctx, "$slug$.fix_error") }
			}
		}
		@ui.Form(ui.FormOpts{
			Target: "#$slug$-form", Swap: "innerHTML",
			Attrs: ui.Attrs{
				Class:  "card max-w-lg space-y-4",
				TestID: "$slug$-form",
				HX:     ui.HX{Post: $low$FormAction(d), Disable: true},
			},
		}) {
			@ui.Field(ui.FieldOpts{Name: "name", Label: i18n.T(ctx, "$slug$.field_name"), Error: d.NameErr, Required: true}) {
				@ui.TextInput(ui.TextInputOpts{
					Name: "name", Value: d.Name, Required: true, Error: d.NameErr,
					Placeholder: i18n.T(ctx, "$slug$.field_placeholder"),
				})
			}
			@ui.FormActions(ui.FormActionsOpts{Align: ui.AlignStart}) {
				@ui.Button(ui.ButtonOpts{
					Label: $low$SubmitLabel(ctx, d), Type: ui.TypeSubmit, Action: ui.ActionPrimary,
				})
				// The indicator is a sibling, not a child of the button: htmx
				// flags the requesting element, so any .htmx-indicator inside it
				// shows, and the button keeps an accessible name that is only
				// its label.
				@ui.Spinner(ui.SpinnerOpts{})
				@ui.ButtonLink(ui.ButtonLinkOpts{Label: i18n.T(ctx, "$slug$.cancel"), Href: "$route$"})
			}
		}
	</div>
}

// $Exp$FormPage is the non-htmx 422 target: the form on its own
// page, so a client without JavaScript still sees the validation message.
templ $Exp$FormPage(d $Exp$FormData) {
	@ui.PageHeader(ui.PageHeaderOpts{Title: i18n.T(ctx, "$slug$.title")})
	@$Exp$Form(d)
}

// $low$FormAction posts to the collection for a create and to
// the row for an edit, which is the contract the handlers implement.
func $low$FormAction(d $Exp$FormData) string {
	if d.ID == 0 {
		return "$route$"
	}
	return fmt.Sprintf("$route$/%d", d.ID)
}

func $low$SubmitLabel(ctx context.Context, d $Exp$FormData) string {
	if d.ID == 0 {
		return i18n.T(ctx, "$slug$.create")
	}
	return i18n.T(ctx, "$slug$.save")
}
`

// locales is the module's own translation set. Generation refuses a key missing
// from a locale or one whose format placeholders differ between locales, so
// both catalogs are built from one table of English/Spanish pairs.
func (r resourceSpec) locales() map[string]map[string]string {
	pairs := [][3]string{
		{"admin_nav", "$HumanPlural$", "$HumanPlural$"},
		{"admin_title", "$HumanPlural$ (all organizations)", "$HumanPlural$ (todas las organizaciones)"},
		{"cancel", "Cancel", "Cancelar"},
		{"caption", "$HumanPlural$", "$HumanPlural$"},
		{"clear_search", "Clear search", "Borrar la búsqueda"},
		{"col_actions", "Actions", "Acciones"},
		{"col_created", "Created", "Creado"},
		{"col_name", "Name", "Nombre"},
		{"create", "Create $humanSingular$", "Crear $humanSingular$"},
		{"delete_action", "Delete permanently", "Eliminar definitivamente"},
		{"delete_confirm", "Delete this $humanSingular$? This cannot be undone.",
			"¿Eliminar este $humanSingular$? Esta acción no se puede deshacer."},
		{"delete_row", "Delete %s", "Eliminar %s"},
		{"delete_title", "Delete $humanSingular$", "Eliminar $humanSingular$"},
		{"edit", "Edit", "Editar"},
		{"empty_body", "Add your first $humanSingular$ with the form above.",
			"Añade tu primer $humanSingular$ con el formulario de arriba."},
		{"empty_title", "No $humanPlural$ yet", "Todavía no hay $humanPlural$"},
		{"field_name", "Name", "Nombre"},
		{"field_placeholder", "Name", "Nombre"},
		{"filtered_empty_body", "No $humanSingular$ matches “%s”.", "Ningún $humanSingular$ coincide con «%s»."},
		{"filtered_empty_title", "No matches", "Sin coincidencias"},
		{"fix_error", "Please fix the error below.", "Corrige el error que aparece a continuación."},
		{"nav", "$HumanPlural$", "$HumanPlural$"},
		{"pagination_page", "Page %d", "Página %d"},
		{"save", "Save changes", "Guardar cambios"},
		{"search_label", "Search $humanPlural$", "Buscar $humanPlural$"},
		{"search_placeholder", "Search $humanPlural$…", "Buscar $humanPlural$…"},
		{"title", "$HumanPlural$", "$HumanPlural$"},
		{"toolbar_label", "$HumanSingular$ list controls", "Controles de la lista de $humanPlural$"},
	}
	en := map[string]string{}
	es := map[string]string{}
	for _, pair := range pairs {
		if !r.admin && strings.HasPrefix(pair[0], "admin_") {
			continue
		}
		en[r.slug+"."+pair[0]] = r.expand(pair[1])
		es[r.slug+"."+pair[0]] = r.expand(pair[2])
	}
	return map[string]map[string]string{"en": en, "es": es}
}

// openapi is the module's slice of the /api/v1 contract. Every declared API
// route needs an operation or generation refuses, which is the point: an
// endpoint cannot ship undocumented.
func (r resourceSpec) openapi() *modkit.OpenAPIContribution {
	schema, tag := r.exported, r.plural
	object := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         map[string]any{"type": "integer", "format": "int64"},
			"name":       map[string]any{"type": "string"},
			"created_at": map[string]any{"type": "string", "format": "date-time"},
			"updated_at": map[string]any{"type": "string", "format": "date-time"},
		},
		"required": []string{"id", "name", "created_at", "updated_at"},
	}
	requestBody := map[string]any{
		"required": true,
		"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{"name": map[string]any{
				"type": "string", "maxLength": resourceNameLimit,
				"description": "Trimmed; required after trimming.",
			}},
			"required": []string{"name"},
		}}},
	}
	single := map[string]any{"application/json": map[string]any{"schema": map[string]any{
		"$ref": "#/components/schemas/" + schema,
	}}}
	errorSchema := map[string]any{"application/json": map[string]any{"schema": map[string]any{
		"$ref": "#/components/schemas/Error",
	}}}
	notFound := map[string]any{"description": "No such " + r.humanSingular + ".", "content": errorSchema}
	// The shared 401/403/429/500 responses are owned by ggg/system/api, which
	// is why --api requires it: a $ref that resolves to nothing is refused
	// before anything is written.
	responses := func(extra map[string]any) json.RawMessage {
		out := map[string]any{
			"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
			"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
			"429": map[string]any{"$ref": "#/components/responses/RateLimited"},
			"500": map[string]any{"$ref": "#/components/responses/InternalError"},
		}
		for code, value := range extra {
			out[code] = value
		}
		return rawJSON(out)
	}
	return &modkit.OpenAPIContribution{
		Tags: []modkit.OpenAPITag{{Name: tag, Description: "The project-local " + r.humanPlural + " collection."}},
		Components: map[string]map[string]json.RawMessage{
			"schemas": {schema: rawJSON(object)},
		},
		Operations: []modkit.OpenAPIOperation{
			{
				RouteID: "api." + r.slug + ".list", OperationID: "list" + r.exports,
				Summary: "List " + r.humanPlural + ".", Tags: []string{tag},
				Parameters: rawJSON([]any{
					map[string]any{"name": "limit", "in": "query", "required": false,
						"description": "Page size. Values outside 1-100 fall back to the default.",
						"schema":      map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 50}},
					map[string]any{"name": "offset", "in": "query", "required": false,
						"description": "Rows to skip; negative values are treated as 0.",
						"schema":      map[string]any{"type": "integer", "minimum": 0, "default": 0}},
				}),
				Responses: responses(map[string]any{
					"200": map[string]any{
						"description": "A page of " + r.humanPlural + ".",
						"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"items": map[string]any{"type": "array",
									"items": map[string]any{"$ref": "#/components/schemas/" + schema}},
								"limit":  map[string]any{"type": "integer"},
								"offset": map[string]any{"type": "integer"},
							},
							"required": []string{"items", "limit", "offset"},
						}}},
					},
				}),
			},
			{
				RouteID: "api." + r.slug + ".create", OperationID: "create" + r.exported,
				Summary: "Create one " + r.humanSingular + ".", Tags: []string{tag},
				RequestBody: rawJSON(requestBody),
				Responses: responses(map[string]any{
					"201": map[string]any{"description": "The new " + r.humanSingular + ".", "content": single},
					"400": map[string]any{"$ref": "#/components/responses/InvalidJSON"},
					"422": map[string]any{"$ref": "#/components/responses/ValidationError"},
				}),
			},
			{
				RouteID: "api." + r.slug + ".update", OperationID: "update" + r.exported,
				Summary: "Update one " + r.humanSingular + ".", Tags: []string{tag},
				Parameters:  rawJSON([]any{pathIDParameter()}),
				RequestBody: rawJSON(requestBody),
				Responses: responses(map[string]any{
					"200": map[string]any{"description": "The updated " + r.humanSingular + ".", "content": single},
					"400": map[string]any{"$ref": "#/components/responses/InvalidJSON"},
					"404": notFound,
					"422": map[string]any{"$ref": "#/components/responses/ValidationError"},
				}),
			},
			{
				RouteID: "api." + r.slug + ".delete", OperationID: "delete" + r.exported,
				Summary: "Delete one " + r.humanSingular + ".", Tags: []string{tag},
				Parameters: rawJSON([]any{pathIDParameter()}),
				Responses: responses(map[string]any{
					"204": map[string]any{"description": "The " + r.humanSingular + " is gone."},
					"404": notFound,
				}),
			},
		},
	}
}

func pathIDParameter() map[string]any {
	return map[string]any{
		"name": "id", "in": "path", "required": true,
		"description": "The row id.",
		"schema":      map[string]any{"type": "integer", "format": "int64"},
	}
}

// rawJSON encodes one literal declaration. Every value passed here is a map or
// slice of strings, numbers and booleans, which json.Marshal cannot fail on, so
// an error would be a programming mistake rather than a runtime condition.
func rawJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic("gggcli: openapi declaration is not encodable: " + err.Error())
	}
	return encoded
}
