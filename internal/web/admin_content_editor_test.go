package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The admin editor's body field is a ui.MarkdownEditor and its preview pane is
// a ui.EditorPreview fed by the server. Both of those are new, and both sit on
// the one path in this application where a mistake is a stored XSS: the editor
// is the only place a human types content that is later rendered as HTML.
//
// So this pins the two properties that must survive any future change to the
// editor's presentation:
//
//   - the persisted body is the author's MARKDOWN, byte for byte. A rich-text
//     editor that saved HTML would satisfy every other test in this file while
//     turning stored content into a script surface.
//   - the rendered HTML carries no live markup. goldmark runs without
//     html.WithUnsafe, so raw HTML in the source is dropped, and templ.Raw is
//     only ever handed that output — never an author string.
//
// The payload is the classic pair: a tag whose handler fires with no user
// interaction, and a script element.
const editorXSSBody = "Before\n\n<img src=x onerror=alert(1)>\n\n<script>alert(1)</script>\n\n**After**"

func TestAdminEditorPersistsMarkdownAndEscapesPreview(t *testing.T) {
	s, admin := contentAdmin(t, "cmsxss")

	code, _, _ := postForm(t, s, "/admin/content", url.Values{
		"kind":    {"post"},
		"title":   {"Editor XSS"},
		"slug":    {"editor-xss"},
		"body_md": {editorXSSBody},
	}, admin)
	require.Equal(t, http.StatusOK, code)

	entry, err := s.q.GetEntryByKindSlugLocale(t.Context(), sqlc.GetEntryByKindSlugLocaleParams{
		Kind: "post", Slug: "editor-xss", Locale: "",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.q.DeleteEntry(t.Context(), entry.ID) })

	// 1. Markdown in, Markdown stored.
	assert.Equal(t, editorXSSBody, entry.BodyMd,
		"the editor stores the author's Markdown; storing HTML would make the column a script surface")

	// 2. The rendered HTML has no live markup left in it.
	assertNoLiveMarkup(t, "stored body_html", entry.BodyHtml)

	// 3. The editor page's preview pane shows the same escaped output. This is
	//    the pane the author reads, and it is the one that reaches templ.Raw.
	_, _, page := serve(t, s, "GET", fmt.Sprintf("/admin/content/%d", entry.ID), nil, nil, admin)
	pane := sliceBetween(page, `data-testid="content-preview"`, `</div>`)
	require.NotEmpty(t, pane, "the editor must render a preview pane")
	assertNoLiveMarkup(t, "editor preview pane", pane)
	assert.Contains(t, pane, "raw HTML omitted",
		"goldmark drops raw HTML rather than trusting it, and says so")
	t.Logf("editor preview pane renders:\n%s", pane)

	// 4. The live preview endpoint the textarea debounces into agrees. A second
	//    renderer here would be a second set of escaping rules.
	code, _, live := postForm(t, s, "/admin/content/preview", url.Values{
		"kind":    {"post"},
		"body_md": {editorXSSBody},
	}, admin)
	require.Equal(t, http.StatusOK, code)
	assertNoLiveMarkup(t, "live preview fragment", live)
	assert.Contains(t, live, "<strong>After</strong>", "real Markdown still renders")
	t.Logf("live preview fragment renders:\n%s", strings.TrimSpace(live))
}

// assertNoLiveMarkup fails if a rendered fragment carries anything the browser
// would execute or fetch. Asserting on the escaped form as well as the absence
// of the live one is deliberate: a fragment that dropped the text entirely
// would pass a bare NotContains while silently losing the author's content.
func assertNoLiveMarkup(t *testing.T, what, html string) {
	t.Helper()
	assert.NotContains(t, html, "<script", "%s must not carry a script element", what)
	assert.NotContains(t, html, "onerror=", "%s must not carry an inline event handler", what)
	assert.NotContains(t, html, "<img src=x", "%s must not carry a live img element", what)
}

// sliceBetween returns the substring starting at marker and ending at the first
// end after it, or "" when the marker is absent.
func sliceBetween(body, marker, end string) string {
	at := strings.Index(body, marker)
	if at < 0 {
		return ""
	}
	rest := body[at:]
	if stop := strings.Index(rest, end); stop >= 0 {
		return rest[:stop+len(end)]
	}
	return rest
}

// The support role reads the editor and is offered nothing that writes. This
// is the same boundary roles_test.go asserts for the list, checked on the
// editor because its controls moved into a ui.FormActions block behind one
// AdminWrite branch — one wrong brace would expose every one of them.
func TestSupportSeesEditorWithoutWriteControls(t *testing.T) {
	s := integrationServer(t, nil)
	support := staffUser(t, s, "user_edsup", "org_edsup", identity.RoleSupport)
	admin := staffUser(t, s, "user_edadm", "org_edadm", identity.RoleAdmin)

	code, _, _ := postForm(t, s, "/admin/content", url.Values{
		"kind": {"post"}, "title": {"Editor roles"}, "slug": {"editor-roles"}, "body_md": {"body"},
	}, admin)
	require.Equal(t, http.StatusOK, code)
	entry, err := s.q.GetEntryByKindSlugLocale(t.Context(), sqlc.GetEntryByKindSlugLocaleParams{
		Kind: "post", Slug: "editor-roles", Locale: "",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.q.DeleteEntry(t.Context(), entry.ID) })

	path := fmt.Sprintf("/admin/content/%d", entry.ID)
	_, _, supportBody := serve(t, s, "GET", path, nil, nil, support)
	_, _, adminBody := serve(t, s, "GET", path, nil, nil, admin)

	require.Contains(t, adminBody, `data-testid="content-save"`, "an admin is offered the save control")
	for _, id := range []string{"content-save", "content-publish-", "content-delete-", "content-restore-"} {
		assert.NotContains(t, supportBody, id, "support must not be offered %q", id)
	}
	// …but the content itself is readable, or the assertions above would pass
	// for the wrong reason.
	assert.Contains(t, supportBody, `data-testid="content-editor-form"`)
	assert.Contains(t, supportBody, "Editor roles")
}
