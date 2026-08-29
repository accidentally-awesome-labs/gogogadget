// Test-only surface owned by the test-only module page/test-content-guide, which
// lives under registry/testdata and is never selectable in production.
//
// This is a non-test file on purpose: it declares a content type and its routes
// the same way a real module would, which is the whole point — the extensibility
// claim is only proved if the fixture goes through the same path a shipped module
// does. Registration is gated behind Deps.TestOnlyModules, which web.NewModule
// (the constructor the generated bootstrap calls) never sets, so a booted
// production runtime cannot reach these paths.
package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/content"
)

// testOnlyGuideType is a content type that exists nowhere in the production
// tree. It proves a new type gets an admin surface, an editor with its declared
// field, validation, public pages, and sitemap membership with no migration, no
// table, no handler, and no template written for it.
func testOnlyGuideType() content.Type {
	return content.Type{
		Kind: "guide", LabelKey: "content.type.post", PluralKey: "content.type.posts",
		Path: "/guides", Mode: content.ModePages, Slug: content.SlugFromTitle, Sitemap: true,
		Fields: []content.Field{{
			Key: "level", LabelKey: "content.field.author",
			Kind: content.FieldSelect, Required: true, Options: []string{"intro", "advanced"},
		}},
	}
}

// testOnlyContentTypes returns every content type contributed by a test-only
// module. Production never calls this.
func testOnlyContentTypes() []content.Type {
	return []content.Type{testOnlyGuideType()}
}

// testOnlyRoutes mirrors what the route emitter produces for a content type
// declaration: one index route, plus a detail route for ModePages.
func testOnlyRoutes() []Route {
	routes := make([]Route, 0, 2)
	for _, t := range testOnlyContentTypes() {
		if t.Path == "" {
			continue
		}
		kind := t.Kind
		routes = append(routes, Route{
			ID: "content." + kind + ".index", Method: "GET", Pattern: t.Path,
			Scope:   ScopePublic,
			Handler: func(s *Server) http.Handler { return s.handleContentIndex(kind) },
		})
		if t.Mode != content.ModePages {
			continue
		}
		routes = append(routes, Route{
			ID: "content." + kind + ".detail", Method: "GET", Pattern: t.Path + "/{slug}",
			Scope:   ScopePublic,
			Handler: func(s *Server) http.Handler { return s.handleContentDetail(kind) },
		})
	}
	return routes
}
