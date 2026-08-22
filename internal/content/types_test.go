package content

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The registry is the only validator a content type gets — kind carries no
// CHECK constraint in the database on purpose. Every rejection must name the
// offending value, because the failure surfaces at boot with no request to
// point at.
func TestNewRegistryRejectsBadDeclarations(t *testing.T) {
	valid := Type{Kind: "guide", LabelKey: "l", PluralKey: "p", Mode: ModePages}

	for name, tc := range map[string]struct {
		types []Type
		names string
	}{
		"duplicate kind": {
			types: []Type{valid, valid},
			names: "guide",
		},
		"invalid kind charset": {
			types: []Type{{Kind: "Guide-1", LabelKey: "l", PluralKey: "p", Mode: ModePages}},
			names: "Guide-1",
		},
		"missing plural key": {
			types: []Type{{Kind: "guide", LabelKey: "l", Mode: ModePages}},
			names: "guide",
		},
		"unknown mode": {
			types: []Type{{Kind: "guide", LabelKey: "l", PluralKey: "p", Mode: "carousel"}},
			names: "carousel",
		},
		"select with no options": {
			types: []Type{{Kind: "guide", LabelKey: "l", PluralKey: "p", Mode: ModePages,
				Fields: []Field{{Key: "level", LabelKey: "f", Kind: FieldSelect}}}},
			names: "level",
		},
		"reserved path": {
			types: []Type{{Kind: "guide", LabelKey: "l", PluralKey: "p", Mode: ModePages, Path: "/admin"}},
			names: "/admin",
		},
		// Nesting under a reserved prefix is the subtle one: Go's mux prefers a
		// literal over a wildcard, so "/docs/guides" would silently hide the
		// "guides" doc page instead of failing at boot.
		"path nested under a reserved prefix": {
			types: []Type{{Kind: "guide", LabelKey: "l", PluralKey: "p", Mode: ModePages, Path: "/docs/guides"}},
			names: "/docs",
		},
		"path nested under the static prefix": {
			types: []Type{{Kind: "guide", LabelKey: "l", PluralKey: "p", Mode: ModePages, Path: "/static/guides"}},
			names: "/static",
		},
		"path without leading slash": {
			types: []Type{{Kind: "guide", LabelKey: "l", PluralKey: "p", Mode: ModePages, Path: "guides"}},
			names: "guides",
		},
		"two types on one path": {
			types: []Type{
				{Kind: "guide", LabelKey: "l", PluralKey: "p", Mode: ModePages, Path: "/guides"},
				{Kind: "manual", LabelKey: "l", PluralKey: "p", Mode: ModePages, Path: "/guides"},
			},
			names: "/guides",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewRegistry(tc.types)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.names,
				"the error must name the offending value, or a boot failure is unfixable")
		})
	}
}

func TestDefaultTypesBuildAndResolve(t *testing.T) {
	reg, err := NewRegistry(DefaultTypes())
	require.NoError(t, err)
	require.Len(t, reg.All(), 2)

	post, ok := reg.Get("post")
	require.True(t, ok)
	assert.Equal(t, "/blog", post.Path)
	assert.Equal(t, ModePages, post.Mode)
	assert.True(t, post.Feed)

	release, ok := reg.Get("release")
	require.True(t, ok)
	assert.Equal(t, ModeSinglePage, release.Mode)
	assert.False(t, release.Feed, "a changelog is read on its page, not subscribed to")

	byPath, ok := reg.ByPath("/changelog")
	require.True(t, ok)
	assert.Equal(t, "release", byPath.Kind)

	_, ok = reg.Get("nope")
	assert.False(t, ok)
	_, ok = reg.ByPath("/nope")
	assert.False(t, ok)
}

// A type with no public path is legal: admin CRUD plus programmatic reads is
// how in-app copy is managed.
func TestRegistryAcceptsPathlessType(t *testing.T) {
	_, err := NewRegistry([]Type{{Kind: "copy", LabelKey: "l", PluralKey: "p", Mode: ModePages}})
	assert.NoError(t, err)
}

func TestSlugFromTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Hello, World!":              "hello-world",
		"  Spaces   everywhere ":     "spaces-everywhere",
		"Already-hyphenated":         "already-hyphenated",
		"Symbols +++ collapse":       "symbols-collapse",
		"---leading and trailing---": "leading-and-trailing",
		"CAPS 123":                   "caps-123",
		"!!!":                        "",
	} {
		assert.Equal(t, want, SlugFromTitle(in, time.Time{}), "slug of %q", in)
	}

	long := SlugFromTitle(strings200(), time.Time{})
	assert.LessOrEqual(t, len(long), 200)
}

func strings200() string {
	s := ""
	for range 60 {
		s += "abcd "
	}
	return s
}

// A changelog entry is identified by its day, in UTC — a local-time slug
// would name a different release depending on where the editor sits.
func TestSlugFromDateIsUTC(t *testing.T) {
	at := time.Date(2026, 8, 19, 23, 30, 0, 0, time.FixedZone("ahead", 3*60*60))
	assert.Equal(t, "2026-08-19", SlugFromDate("ignored", at.UTC()))
	assert.Equal(t, "2026-08-19", SlugFromDate("ignored", at))
}

func TestTypeSlugOfFallsBackToTitle(t *testing.T) {
	t.Run("declared", func(t *testing.T) {
		tp := Type{Slug: SlugFromDate}
		assert.Equal(t, "2026-01-02", tp.SlugOf("Anything", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)))
	})
	t.Run("unset", func(t *testing.T) {
		tp := Type{}
		assert.Equal(t, "some-title", tp.SlugOf("Some Title", time.Time{}))
	})
}

func TestFieldLimitDefaults(t *testing.T) {
	assert.Equal(t, 300, Field{Kind: FieldText}.Limit())
	assert.Equal(t, 5000, Field{Kind: FieldTextarea}.Limit())
	assert.Equal(t, 100, Field{Kind: FieldText, MaxLen: 100}.Limit())
}
