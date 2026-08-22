package content

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Mode controls how a type's entries are published as pages.
type Mode string

const (
	// ModePages: an index at Path listing entries, plus Path/{slug} per entry.
	ModePages Mode = "pages"
	// ModeSinglePage: every entry on one scrollable page at Path, each with a
	// stable anchor. No per-entry URL. This is how a changelog is read.
	ModeSinglePage Mode = "single"
)

// FieldKind is the editor control rendered for a declared field.
type FieldKind string

const (
	FieldText     FieldKind = "text"
	FieldTextarea FieldKind = "textarea"
	FieldURL      FieldKind = "url"
	FieldBool     FieldKind = "bool"
	FieldSelect   FieldKind = "select"
)

// Field is one type-specific value stored in content_entries.meta. Values are
// always strings; FieldBool stores "true"/"false".
type Field struct {
	Key      string // [a-z][a-z0-9_]*, the meta key and the form input name
	LabelKey string // i18n key, present in every catalog
	Kind     FieldKind
	Required bool
	MaxLen   int      // 0 → 300 for text/url/select, 5000 for textarea
	Options  []string // FieldSelect only; each option's label key is LabelKey+"_"+option
}

// Limit is the effective maximum length for this field's value.
func (f Field) Limit() int {
	if f.MaxLen > 0 {
		return f.MaxLen
	}
	if f.Kind == FieldTextarea {
		return 5000
	}
	return 300
}

// SlugFunc derives the default slug offered for a new entry.
type SlugFunc func(title string, publishedAt time.Time) string

// Type declares a content collection. Registering one requires no migration
// and no new table: it is a Go value appended to web.Deps.ContentTypes.
type Type struct {
	Kind      string // [a-z][a-z0-9_]*, stored in content_entries.kind
	LabelKey  string // i18n key, singular ("Post")
	PluralKey string // i18n key, plural ("Posts") — admin filters and buttons
	// Path is the public base URL ("/blog"). Empty means the type has no
	// public routes: admin-managed content read programmatically from any
	// handler or template (in-app copy, help panels, legal snippets).
	Path    string
	Mode    Mode
	Fields  []Field
	Slug    SlugFunc // nil → SlugFromTitle
	Feed    bool     // include entries in /rss.xml
	Sitemap bool     // include in /sitemap.xml
}

// SlugOf applies the type's slug function (SlugFromTitle when unset).
func (t Type) SlugOf(title string, at time.Time) string {
	if t.Slug != nil {
		return t.Slug(title, at)
	}
	return SlugFromTitle(title, at)
}

// Field returns the declared field with this key.
func (t Type) Field(key string) (Field, bool) {
	for _, f := range t.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

var slugSepRe = regexp.MustCompile(`[^a-z0-9]+`)

// SlugFromTitle lowercases, collapses every non-alphanumeric run to a single
// hyphen, trims the ends, and caps the result at 200 characters.
func SlugFromTitle(title string, _ time.Time) string {
	s := slugSepRe.ReplaceAllString(strings.ToLower(title), "-")
	s = strings.Trim(s, "-")
	if len(s) > 200 {
		s = strings.Trim(s[:200], "-")
	}
	return s
}

// SlugFromDate is the changelog shape: one entry per calendar day, in UTC.
func SlugFromDate(_ string, at time.Time) string {
	return at.UTC().Format("2006-01-02")
}

// DefaultTypes returns the built-in types: blog posts and changelog releases.
func DefaultTypes() []Type {
	return []Type{
		{
			Kind: "post", LabelKey: "content.type.post", PluralKey: "content.type.posts",
			Path: "/blog", Mode: ModePages, Slug: SlugFromTitle, Feed: true, Sitemap: true,
			Fields: []Field{{Key: "author", LabelKey: "content.field.author", Kind: FieldText, MaxLen: 100}},
		},
		{
			Kind: "release", LabelKey: "content.type.release", PluralKey: "content.type.releases",
			Path: "/changelog", Mode: ModeSinglePage, Slug: SlugFromDate, Feed: false, Sitemap: true,
		},
	}
}

// reservedPaths are the URL prefixes the app already owns. A type claiming one
// — or anything nested under it — would either shadow a real page or be
// shadowed by it, because Go's mux gives a literal pattern precedence over the
// wildcard that serves the prefix today ("/docs/guides" beats "/docs/{slug}").
// The registry refuses and names the collision. "/" is exact-only: every path
// starts with it.
var reservedPaths = []string{"/", "/admin", "/api", "/app", "/debug", "/dev", "/docs",
	"/favicon.ico", "/healthz", "/ingest", "/login", "/logout", "/media", "/metrics",
	"/pricing", "/privacy", "/readyz", "/robots.txt", "/rss.xml", "/set-locale",
	"/set-theme", "/signup", "/sitemap.xml", "/static", "/terms", "/webhooks"}

var identRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Registry is an immutable, validated lookup built once at boot.
type Registry struct {
	types []Type
	byKey map[string]int
}

// NewRegistry validates every declaration and returns an error naming the
// offending value. It never panics: a bad type is a boot failure, not a crash
// halfway through a request.
func NewRegistry(types []Type) (*Registry, error) {
	r := &Registry{types: make([]Type, 0, len(types)), byKey: make(map[string]int, len(types))}
	paths := map[string]string{}
	for _, t := range types {
		if !identRe.MatchString(t.Kind) {
			return nil, fmt.Errorf("content type kind %q must match [a-z][a-z0-9_]*", t.Kind)
		}
		if _, dup := r.byKey[t.Kind]; dup {
			return nil, fmt.Errorf("content type kind %q is declared twice", t.Kind)
		}
		if t.LabelKey == "" || t.PluralKey == "" {
			return nil, fmt.Errorf("content type %q needs both LabelKey and PluralKey", t.Kind)
		}
		if t.Mode != ModePages && t.Mode != ModeSinglePage {
			return nil, fmt.Errorf("content type %q has unknown mode %q", t.Kind, t.Mode)
		}
		if t.Path != "" {
			if !strings.HasPrefix(t.Path, "/") {
				return nil, fmt.Errorf("content type %q path %q must start with /", t.Kind, t.Path)
			}
			for _, reserved := range reservedPaths {
				if t.Path == reserved || (reserved != "/" && strings.HasPrefix(t.Path, reserved+"/")) {
					return nil, fmt.Errorf("content type %q path %q collides with reserved path %q", t.Kind, t.Path, reserved)
				}
			}
			if other, taken := paths[t.Path]; taken {
				return nil, fmt.Errorf("content type %q path %q is already used by %q", t.Kind, t.Path, other)
			}
			paths[t.Path] = t.Kind
		}
		seen := map[string]bool{}
		for _, f := range t.Fields {
			if !identRe.MatchString(f.Key) {
				return nil, fmt.Errorf("content type %q field key %q must match [a-z][a-z0-9_]*", t.Kind, f.Key)
			}
			if seen[f.Key] {
				return nil, fmt.Errorf("content type %q declares field %q twice", t.Kind, f.Key)
			}
			seen[f.Key] = true
			if f.LabelKey == "" {
				return nil, fmt.Errorf("content type %q field %q needs a LabelKey", t.Kind, f.Key)
			}
			switch f.Kind {
			case FieldText, FieldTextarea, FieldURL, FieldBool:
			case FieldSelect:
				if len(f.Options) == 0 {
					return nil, fmt.Errorf("content type %q field %q is a select with no options", t.Kind, f.Key)
				}
			default:
				return nil, fmt.Errorf("content type %q field %q has unknown kind %q", t.Kind, f.Key, f.Kind)
			}
		}
		r.byKey[t.Kind] = len(r.types)
		r.types = append(r.types, t)
	}
	return r, nil
}

// Get returns the declaration for a kind.
func (r *Registry) Get(kind string) (Type, bool) {
	i, ok := r.byKey[kind]
	if !ok {
		return Type{}, false
	}
	return r.types[i], true
}

// All returns every type in declaration order.
func (r *Registry) All() []Type { return r.types }

// ByPath returns the type served at a public base path.
func (r *Registry) ByPath(path string) (Type, bool) {
	for _, t := range r.types {
		if t.Path != "" && t.Path == path {
			return t, true
		}
	}
	return Type{}, false
}
