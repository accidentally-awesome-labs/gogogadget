// Package ui is the leaf component package. Every reusable presentation
// component lives here. It imports only templ and stdlib leaf packages — never
// internal/web/templates, billing, identity, sqlc, or any domain package. The
// templates package imports ui; the compiler enforces that direction.
package ui

// Kind is the semantic colour axis shared by Badge, Notice and Banner.
type Kind string

const (
	KindBrand   Kind = "brand"
	KindInfo    Kind = "info"
	KindSuccess Kind = "success"
	KindWarn    Kind = "warn"
	KindDanger  Kind = "danger"
	KindNeutral Kind = "neutral"
)

// Kinds is every semantic kind, in severity order.
var Kinds = []Kind{KindBrand, KindInfo, KindSuccess, KindWarn, KindDanger, KindNeutral}

// NavTarget is the only element a navigation may swap.
const NavTarget = "#content"

// NavSwap replaces NavTarget with a view-transition cross-fade and scrolls to
// the top of the new content.
const NavSwap = "outerHTML transition:true show:top"
