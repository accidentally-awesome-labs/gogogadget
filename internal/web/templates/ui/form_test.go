package ui

import (
	"bytes"
	"context"
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renderInContext is renderComponent with a request context, which is where the
// CSRF token lives: the field is published by middleware, not passed as an
// option, so a bare context legitimately renders nothing.
func renderInContext(t *testing.T, ctx context.Context, c templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, c.Render(ctx, &buf))
	return buf.String()
}

// The hidden token is the whole no-script path: the header htmx sends is
// unreachable when htmx never loaded, so without this field the browser's own
// submit is refused with 403 by a form that looks perfectly functional.
func TestFormCarriesTheTokenForUnsafeMethods(t *testing.T) {
	ctx := WithCSRFToken(context.Background(), "masked-token")

	html := renderInContext(t, ctx, Form(FormOpts{Method: "post", Action: "/save"}))

	assert.Contains(t, html, `<input type="hidden" name="`+CSRFFieldName+`" value="masked-token">`)
}

// A GET form is not checked, and a token in a GET is a token in the URL bar, the
// history, the Referer header and every access log it passes through.
func TestFormOmitsTheTokenForSafeMethods(t *testing.T) {
	ctx := WithCSRFToken(context.Background(), "masked-token")

	html := renderInContext(t, ctx, Form(FormOpts{Method: "get", Action: "/search"}))

	assert.NotContains(t, html, CSRFFieldName)
}

// Outside a request there is no token. Emitting value="" would claim to carry
// one and fail as a *bad* token, sending whoever reads the 403 after an expired
// session rather than a missing middleware.
func TestFormRendersNoEmptyTokenField(t *testing.T) {
	html := renderComponent(t, Form(FormOpts{Method: "post", Action: "/save"}))

	assert.NotContains(t, html, CSRFFieldName)
}

// An emitted hx-target="" is not the absence of a target: htmx reads the
// attribute as present and swaps into nothing, so a form that wanted no htmx
// behaviour at all posts into a void.
func TestFormOmitsAttributesItWasNotGiven(t *testing.T) {
	html := renderComponent(t, Form(FormOpts{}))

	for _, absent := range []string{`hx-target=""`, `hx-swap=""`, `method=""`, `action=""`} {
		assert.NotContains(t, html, absent)
	}
}

func TestFormEmitsTheAttributesItWasGiven(t *testing.T) {
	html := renderComponent(t, Form(FormOpts{
		Method: "post", Action: "/save", Target: "#region", Swap: "innerHTML",
	}))

	assert.Contains(t, html, `method="post"`)
	assert.Contains(t, html, `action="/save"`)
	assert.Contains(t, html, `hx-target="#region"`)
	assert.Contains(t, html, `hx-swap="innerHTML"`)
	// Server validation is authoritative; the browser must not block the
	// submission before the server's rules can run.
	assert.Contains(t, html, "novalidate")
}

// An explicit token wins over the context's, for the rare caller that already
// holds one; the empty default is what every ordinary caller uses.
func TestCSRFFieldPrefersAnExplicitToken(t *testing.T) {
	ctx := WithCSRFToken(context.Background(), "from-context")

	html := renderInContext(t, ctx, CSRFField(CSRFFieldOpts{Token: "explicit"}))

	assert.Contains(t, html, `value="explicit"`)
	assert.NotContains(t, html, "from-context")
}
