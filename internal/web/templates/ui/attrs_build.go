package ui

import (
	"encoding/json"
	"strings"

	"github.com/a-h/templ"
)

// attributes flattens Attrs into a single templ.Attributes map spread onto a
// component's root element. The component's own semantic attributes (role,
// aria-*, type) are merged in by the caller after this call; callers cannot
// override them through Attrs.
func attributes(a Attrs) templ.Attributes {
	out := templ.Attributes{}
	if a.ID != "" {
		out["id"] = a.ID
	}
	if a.Class != "" {
		out["class"] = a.Class
	}
	if a.TestID != "" {
		out["data-testid"] = a.TestID
	}
	if a.Title != "" {
		out["title"] = a.Title
	}
	for k, v := range a.Data {
		if k == "ui" {
			// data-ui is reserved for the component registry.
			continue
		}
		out["data-"+k] = v
	}
	applyAlpine(out, a.Alpine)
	applyHX(out, a.HX)
	return out
}

func applyAlpine(out templ.Attributes, al Alpine) {
	if al.Data != "" {
		out["x-data"] = al.Data
	}
	if al.Show != "" {
		out["x-show"] = al.Show
	}
	if al.Text != "" {
		out["x-text"] = al.Text
	}
	if al.Model != "" {
		out["x-model"] = al.Model
	}
	if al.Ref != "" {
		out["x-ref"] = al.Ref
	}
	if al.Trap != "" {
		out["x-trap"] = al.Trap
	}
	if al.If != "" {
		out["x-if"] = al.If
	}
	if al.For != "" {
		out["x-for"] = al.For
	}
	if al.Key != "" {
		out["x-key"] = al.Key
	}
	if al.Cloak {
		out["x-cloak"] = true
	}
	for k, v := range al.Bind {
		out[":"+k] = v
	}
	for k, v := range al.On {
		out["@"+k] = v
	}
}

func applyHX(out templ.Attributes, hx HX) {
	if hx.Get != "" {
		out["hx-get"] = hx.Get
	}
	if hx.Post != "" {
		out["hx-post"] = hx.Post
	}
	if hx.Put != "" {
		out["hx-put"] = hx.Put
	}
	if hx.Patch != "" {
		out["hx-patch"] = hx.Patch
	}
	if hx.Delete != "" {
		out["hx-delete"] = hx.Delete
	}
	if hx.Target != "" {
		out["hx-target"] = hx.Target
	}
	if hx.Select != "" {
		out["hx-select"] = hx.Select
	}
	if hx.Swap != "" {
		out["hx-swap"] = hx.Swap
	}
	if hx.Trigger != "" {
		out["hx-trigger"] = hx.Trigger
	}
	if len(hx.Vals) > 0 {
		if encoded, err := json.Marshal(hx.Vals); err == nil {
			out["hx-vals"] = string(encoded)
		}
	}
	if len(hx.Headers) > 0 {
		if encoded, err := json.Marshal(hx.Headers); err == nil {
			out["hx-headers"] = string(encoded)
		}
	}
	if hx.Confirm != "" {
		out["hx-confirm"] = hx.Confirm
	}
	if hx.Encoding != "" {
		out["hx-encoding"] = hx.Encoding
	}
	if hx.Include != "" {
		out["hx-include"] = hx.Include
	}
	if hx.Sync != "" {
		out["hx-sync"] = hx.Sync
	}
	// The indicator is a selector, and htmx expects one. Callers hold an id, so
	// a bare id would silently match nothing - prefix it here rather than making
	// every call site remember the "#".
	if hx.Indicator != "" {
		out["hx-indicator"] = indicatorSelector(hx.Indicator)
	}
	if hx.PushURL != "" {
		out["hx-push-url"] = hx.PushURL
	}
	if hx.Boost {
		out["hx-boost"] = "true"
	}
	if hx.Disable {
		out["hx-disable"] = "this"
	}
	if hx.HistoryElt {
		out["hx-history-elt"] = true
	}
}

// indicatorSelector accepts either a bare id or a selector. Every call site in
// the catalog holds an element id, and htmx needs a selector, so the common case
// is prefixed here. A value that is already a selector is passed through
// untouched: prefixing "#foo .spinner" or ".loading" would produce a selector
// that matches nothing, and a request indicator that never appears is invisible
// to test and reader alike.
func indicatorSelector(value string) string {
	if strings.ContainsAny(value, "#.[ >:") {
		return value
	}
	return "#" + value
}
