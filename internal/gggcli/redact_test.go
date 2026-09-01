package gggcli

import (
	"strings"
	"testing"
)

// Declared secret values never survive a render: the redactor masks them at
// the single render boundary.
func TestRedactorMasksDeclaredSecretValues(t *testing.T) {
	r := NewRedactor()
	r.RegisterSecret("RESEND_API_KEY", "sk-live-abcdef0123456789")
	r.RegisterSecret("SENTRY_DSN", "https://sentry.io/123")
	r.RegisterSecret("SHORT", "abc")

	if got := r.Apply("token sk-live-abcdef0123456789 in message"); got != "token [redacted] in message" {
		t.Fatalf("Apply = %q", got)
	}
	// Longest-first masking leaves no fragment of a value contained in another.
	if got := r.Apply("value abc and abcdef"); strings.Contains(got, "abc") && got != "value [redacted] and [redacted]ef" {
		t.Fatalf("Apply = %q", got)
	}
	payload := map[string]any{
		"RESEND_API_KEY": "sk-live-abcdef0123456789",
		"nested":         map[string]any{"SENTRY_DSN": []any{"https://sentry.io/123"}},
	}
	r.ApplyPayload(payload)
	if payload["RESEND_API_KEY"] != "[redacted]" {
		t.Fatalf("payload secret survived: %#v", payload)
	}
	nested := payload["nested"].(map[string]any)
	list := nested["SENTRY_DSN"].([]any)
	if list[0] != "[redacted]" {
		t.Fatalf("nested secret survived: %#v", list)
	}
	// Empty values are never registered: masking the empty string would
	// corrupt output.
	empty := NewRedactor()
	empty.RegisterSecret("EMPTY", "")
	if got := empty.Apply("plain text"); got != "plain text" {
		t.Fatalf("empty secret corrupted output: %q", got)
	}
}
