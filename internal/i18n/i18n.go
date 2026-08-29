// Package i18n is the localization seam: detection middleware, the per-request
// printer, and the T() lookup every template uses for user-visible strings.
// Catalogs live in catalog_en.go / catalog_es.go; keys are namespaced
// ("nav.projects") so a missing translation can never silently render as a
// legitimate display string.
package i18n

import (
	"context"
	"net/http"
	"sync"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// CookieName is the persisted locale preference (set only by POST /set-locale).
const CookieName = "ggg_lang"

// The matcher is built from the generated locale list so it can never resolve a
// request to a language nothing is translated into, and the switcher can never
// offer one. Both used to be hand-written and had to be kept in step by hand.
var matcher = language.NewMatcher(supportedTags())

func supportedTags() []language.Tag {
	tags := make([]language.Tag, 0, len(Locales))
	for _, l := range Locales {
		tags = append(tags, l.Tag)
	}
	return tags
}

// DefaultTag is the first declared locale, which is the fallback everywhere a
// request carries no usable preference.
func DefaultTag() language.Tag {
	if len(Locales) == 0 {
		return language.English
	}
	return Locales[0].Tag
}

type state struct {
	printer *message.Printer
	tag     language.Tag
}

type ctxKey struct{}

var (
	defaultState = state{printer: message.NewPrinter(language.English), tag: language.English}

	printersMu sync.Mutex
	printers   = map[language.Tag]*message.Printer{}
)

func printerFor(tag language.Tag) *message.Printer {
	printersMu.Lock()
	defer printersMu.Unlock()
	if p, ok := printers[tag]; ok {
		return p
	}
	p := message.NewPrinter(tag)
	printers[tag] = p
	return p
}

// Detect resolves the request locale (?lang= → ggg_lang cookie →
// Accept-Language → English), stores the printer+tag in the request context,
// and never writes cookies. Preference persistence is /set-locale's job.
func Detect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieVal := ""
		if c, err := r.Cookie(CookieName); err == nil {
			cookieVal = c.Value
		}
		// MatchStrings: candidates in priority order; each parsed as
		// Accept-Language; first non-No-confidence match wins; default en.
		tag, _ := language.MatchStrings(matcher, r.URL.Query().Get("lang"), cookieVal, r.Header.Get("Accept-Language"))
		next.ServeHTTP(w, r.WithContext(withState(r.Context(), state{printer: printerFor(tag), tag: tag})))
	})
}

// WithTag injects a locale into an arbitrary context (mail rendering outside
// HTTP). Zero-value tag falls back to English.
func WithTag(ctx context.Context, tag language.Tag) context.Context {
	if tag == language.Und {
		tag = language.English
	}
	return withState(ctx, state{printer: printerFor(tag), tag: tag})
}

func withState(ctx context.Context, s state) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

func stateFrom(ctx context.Context) state {
	if s, ok := ctx.Value(ctxKey{}).(state); ok {
		return s
	}
	return defaultState
}

// T looks up key in the request locale, formatting with args. A missing
// translation (Sprintf returns the key itself) retries English; a key missing
// everywhere renders verbatim — namespaced keys make that a visible bug, not a
// silent wrong string.
func T(ctx context.Context, key string, args ...any) string {
	s := stateFrom(ctx)
	out := s.printer.Sprintf(key, args...)
	if out == key && s.tag != language.English {
		if retry := defaultState.printer.Sprintf(key, args...); retry != key {
			return retry
		}
	}
	return out
}

// Tag returns the resolved locale (English when absent).
func Tag(ctx context.Context) language.Tag {
	return stateFrom(ctx).tag
}

// ParseSupported maps a switcher code to its tag. ok is false for anything not
// declared by an installed module.
func ParseSupported(code string) (language.Tag, bool) {
	for _, l := range Locales {
		if l.Code == code {
			return l.Tag, true
		}
	}
	return language.Und, false
}

// ParseOrDefault maps a stored locale code ("" = unset) to its tag, defaulting
// to the first declared locale.
func ParseOrDefault(code string) language.Tag {
	if tag, ok := ParseSupported(code); ok {
		return tag
	}
	return DefaultTag()
}
