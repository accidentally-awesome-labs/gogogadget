package gggcli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// RedactionToken is the placeholder a masked secret value renders as.
const RedactionToken = "[redacted]"

// Redactor masks declared secret values in every rendered surface: prompts,
// plans, diagnostics, JSON, logs, and state dumps. The CLI registers two
// sources: declared secret environment keys whose values are present in the
// process environment, and secret values handed over by provider/deploy
// commands through RegisterSecret. Renderers apply it at the single render
// boundary, so a new command cannot forget to redact.
type Redactor struct {
	values map[string]string
}

// NewRedactor returns an empty redactor.
func NewRedactor() *Redactor {
	return &Redactor{values: map[string]string{}}
}

// RegisterSecret masks every occurrence of value, attributing it to key.
// Empty values are ignored: masking the empty string would corrupt output.
func (r *Redactor) RegisterSecret(key, value string) {
	if key == "" || value == "" {
		return
	}
	r.values[key] = value
}

// Apply masks every registered secret value in s.
func (r *Redactor) Apply(s string) string {
	if len(r.values) == 0 || s == "" {
		return s
	}
	for _, value := range r.orderedValues() {
		if strings.Contains(s, value) {
			s = strings.ReplaceAll(s, value, RedactionToken)
		}
	}
	return s
}

// ApplyEnvelope masks secret values in every diagnostic message and change
// path of the envelope, in place.
func (r *Redactor) ApplyEnvelope(env *modkit.Envelope) {
	for i := range env.Diagnostics {
		env.Diagnostics[i].Message = r.Apply(env.Diagnostics[i].Message)
	}
}

// ApplyPayload walks a result payload and masks every string it finds, in
// place. Map keys are never masked: only values carry secrets.
func (r *Redactor) ApplyPayload(payload map[string]any) {
	if payload == nil {
		return
	}
	for key, value := range payload {
		payload[key] = r.applyValue(value)
	}
}

func (r *Redactor) applyValue(value any) any {
	switch typed := value.(type) {
	case string:
		return r.Apply(typed)
	case *string:
		if typed != nil {
			masked := r.Apply(*typed)
			return &masked
		}
		return typed
	case fmt.Stringer:
		// Stringers render through their String method at marshal time; mask
		// the rendered form eagerly so nothing bypasses the boundary.
		return r.Apply(typed.String())
	case map[string]any:
		r.ApplyPayload(typed)
		return typed
	case []map[string]any:
		for i := range typed {
			r.ApplyPayload(typed[i])
		}
		return typed
	case []string:
		for i := range typed {
			typed[i] = r.Apply(typed[i])
		}
		return typed
	case []any:
		for i := range typed {
			typed[i] = r.applyValue(typed[i])
		}
		return typed
	default:
		return value
	}
}

// orderedValues returns registered values longest-first, so a value that
// contains another is masked before the shorter one leaves a fragment behind.
func (r *Redactor) orderedValues() []string {
	values := make([]string, 0, len(r.values))
	for _, value := range r.values {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values
}
