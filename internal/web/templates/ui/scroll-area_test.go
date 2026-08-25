package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A scrollable div with no tab stop is reachable only by pointer, so once its
// content overflows it is unreadable to a keyboard user: there is nothing for
// the arrow keys to act on.
func TestScrollAreaIsKeyboardScrollable(t *testing.T) {
	html := renderComponent(t, ScrollArea(ScrollAreaOpts{Label: "Release notes", Height: HeightSM}))
	assert.Contains(t, html, `tabindex="0"`)
	assert.Contains(t, html, `role="region"`)
	assert.Contains(t, html, `aria-label="Release notes"`)
	assert.Contains(t, html, "overflow-y-auto")
	assert.Contains(t, html, "max-h-32")
}

// A <section> without an accessible name is not a landmark - it is an anonymous
// div with extra letters - so the attribute is omitted rather than dangling
// when there is nothing to point it at.
func TestSectionIsALandmarkOnlyWhenNamed(t *testing.T) {
	named := renderComponent(t, Section(SectionOpts{
		Title: "Billing", Level: 3, Attrs: Attrs{ID: "billing"},
	}))
	assert.Contains(t, named, `aria-labelledby="billing-title"`)
	assert.Contains(t, named, `id="billing-title"`)
	assert.Contains(t, named, "<h3")

	// No ID means nothing to reference: a dangling aria-labelledby is worse
	// than none, because some assistive technology announces nothing at all.
	anonymous := renderComponent(t, Section(SectionOpts{Title: "Billing"}))
	assert.NotContains(t, anonymous, "aria-labelledby")
}

// Two panes side by side on a phone give each about twenty characters per line.
func TestSplitStacksOnSmallScreens(t *testing.T) {
	html := renderComponent(t, Split(SplitOpts{Ratio: RatioWide, Gap: GapMD}))
	assert.Contains(t, html, "grid-cols-1", "one column is the small-screen default")
	assert.Contains(t, html, "md:grid-cols-3", "the ratio only applies from md up")
	assert.Contains(t, html, "gap-4")
}

// AspectRatio exists to stop layout shift: without a reserved box the page
// reflows when the medium arrives, moving whatever the user was about to click.
func TestAspectRatioReservesTheBox(t *testing.T) {
	assert.Contains(t, renderComponent(t, AspectRatio(AspectRatioOpts{Ratio: RatioVideo})), "aspect-video")
	assert.Contains(t, renderComponent(t, AspectRatio(AspectRatioOpts{Ratio: RatioSquare})), "aspect-square")

	// RatioAuto is the honest name for declining to reserve. Checking for the
	// bare prefix would match this component's own data-ui name.
	auto := renderComponent(t, AspectRatio(AspectRatioOpts{}))
	assert.NotContains(t, auto, "aspect-video")
	assert.NotContains(t, auto, "aspect-square")
	assert.NotContains(t, auto, `class="overflow-hidden "`,
		"an empty ratio must not leave a trailing space in the class list")
}

// Sticky rather than fixed: a fixed bar overlays the viewport, and on a short
// screen it can hide the very field its Save button submits.
func TestStickyBarStaysInItsContainer(t *testing.T) {
	html := renderComponent(t, StickyBar(StickyBarOpts{Side: SideBottom}))
	assert.Contains(t, html, "sticky")
	assert.NotContains(t, html, "fixed")
	assert.Contains(t, html, "bottom-0")

	assert.Contains(t, renderComponent(t, StickyBar(StickyBarOpts{Side: SideTop})), "top-0")
}

// A toolbar role promises arrow-key navigation with one tab stop; these
// controls are each tabbable, so claiming it would advertise a contract that is
// not implemented.
func TestToolbarClaimsGroupNotToolbar(t *testing.T) {
	html := renderComponent(t, Toolbar(ToolbarOpts{Label: "Editor actions"}))
	assert.Contains(t, html, `role="group"`)
	assert.NotContains(t, html, `role="toolbar"`)
	assert.Contains(t, html, `aria-label="Editor actions"`)
}

// A tile with no destination must stay a card: a click handler on a div is
// unreachable by keyboard and announced as nothing.
func TestTileOnlyBecomesALinkWithADestination(t *testing.T) {
	link := renderComponent(t, Tile(TileOpts{Title: "Docs", Href: "/docs", Icon: IconFile}))
	assert.Contains(t, link, `<a href="/docs"`)
	assert.Contains(t, link, `hx-boost="true"`)

	card := renderComponent(t, Tile(TileOpts{Title: "Plain", Body: "No destination"}))
	assert.NotContains(t, card, "<a ")
	assert.NotContains(t, card, "onclick")
}

// A screen reader's link list shows link text out of context, so forty links
// all reading "download" are indistinguishable.
func TestAttachmentLinksTheFileName(t *testing.T) {
	html := renderComponent(t, Attachment(AttachmentOpts{
		Name: "architecture.pdf", Size: "184 KB", Href: "/files/1",
	}))
	assert.Contains(t, html, `<a href="/files/1" class="link truncate">architecture.pdf</a>`)
	assert.NotContains(t, html, ">Download<")
	assert.Contains(t, html, "184 KB")
}

// ValueSlot and Value both fill one slot, so the precedence is stated rather
// than left to whichever field a caller happened to set.
func TestDescriptionValueSlotWinsOverValue(t *testing.T) {
	html := renderComponent(t, DescriptionList(DescriptionListOpts{Items: []DescriptionItem{
		{Term: "Plan", Value: "plain text", ValueSlot: Badge(BadgeOpts{Text: "Pro", Kind: KindBrand})},
	}}))
	assert.Contains(t, html, "Pro")
	assert.NotContains(t, html, "plain text")

	// Copyable adds the control only when there is a value to copy.
	copyable := renderComponent(t, DescriptionList(DescriptionListOpts{Items: []DescriptionItem{
		{Term: "ID", Value: "prj_1", Copyable: true},
	}}))
	assert.Contains(t, copyable, `data-copy="prj_1"`)

	slotOnly := renderComponent(t, DescriptionList(DescriptionListOpts{Items: []DescriptionItem{
		{Term: "Plan", ValueSlot: Badge(BadgeOpts{Text: "Pro"}), Copyable: true},
	}}))
	assert.NotContains(t, slotOnly, "data-copy",
		"there is no text to place on the clipboard")
}
