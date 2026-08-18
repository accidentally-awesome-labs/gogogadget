package i18n

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

func TestTKnownKey(t *testing.T) {
	// errors.not_found is cataloged in both locales (catalog_en.go / catalog_es.go).
	assert.Equal(t, "That page doesn't exist.", T(context.Background(), "errors.not_found"))
	assert.Equal(t, "That page doesn't exist.", T(WithTag(context.Background(), language.English), "errors.not_found"))
}

func TestTSpanishLocale(t *testing.T) {
	// Proves T selects the request locale's catalog, not just English.
	ctx := WithTag(context.Background(), language.Spanish)
	assert.Equal(t, "Esa página no existe.", T(ctx, "errors.not_found"))
}

func TestTFormattingArgs(t *testing.T) {
	// T formats through the locale printer (Sprintf contract).
	assert.Equal(t, "Page 2 of 5", T(context.Background(), "activity.pagination_page_of", 2, 5))
}

func TestTMissingKeyFallback(t *testing.T) {
	// Observed behavior (i18n.go T): the locale printer's Sprintf returns the
	// key itself when no message is registered; if the tag is not English, T
	// retries the English printer; a key missing from every catalog renders
	// VERBATIM as the key string — namespaced keys make that a visible bug
	// rather than a silent wrong translation.
	t.Run("missing everywhere renders the key verbatim", func(t *testing.T) {
		const key = "test.no_such_key_anywhere"
		assert.Equal(t, key, T(context.Background(), key), "default (en) context")
		assert.Equal(t, key, T(WithTag(context.Background(), language.Spanish), key), "es context after en retry also misses")
	})

	t.Run("missing in locale retries English catalog", func(t *testing.T) {
		// Register a key in the English catalog only (test-binary-local) to
		// exercise the en-retry branch without a catalog parity gap.
		const key = "test.en_only_retry_key"
		message.SetString(language.English, key, "English fallback string")
		assert.Equal(t, "English fallback string", T(WithTag(context.Background(), language.Spanish), key))
		assert.Equal(t, "English fallback string", T(context.Background(), key))
	})
}

func TestTagWithTagRoundtrip(t *testing.T) {
	ctx := WithTag(context.Background(), language.Spanish)
	assert.Equal(t, language.Spanish, Tag(ctx))

	ctx = WithTag(context.Background(), language.English)
	assert.Equal(t, language.English, Tag(ctx))
}

func TestTagDefaults(t *testing.T) {
	// Bare context resolves to English (defaultState).
	assert.Equal(t, language.English, Tag(context.Background()))
	// Zero-value tag injected via WithTag also falls back to English.
	assert.Equal(t, language.English, Tag(WithTag(context.Background(), language.Und)))
}

func TestLocales(t *testing.T) {
	require.Len(t, Locales, 2)

	assert.Equal(t, "en", Locales[0].Code)
	assert.Equal(t, "English", Locales[0].Label)
	assert.Equal(t, language.English, Locales[0].Tag)

	assert.Equal(t, "es", Locales[1].Code)
	assert.Equal(t, "Español", Locales[1].Label)
	assert.Equal(t, language.Spanish, Locales[1].Tag)

	// Every advertised locale is accepted by ParseSupported; anything else is not.
	for _, l := range Locales {
		tag, ok := ParseSupported(l.Code)
		assert.True(t, ok, "ParseSupported(%q)", l.Code)
		assert.Equal(t, l.Tag, tag)
	}
	_, ok := ParseSupported("fr")
	assert.False(t, ok)
}
