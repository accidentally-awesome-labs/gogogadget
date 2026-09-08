package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func slides() []SlideData {
	return []SlideData{
		{ID: "s1", Title: "First"},
		{ID: "s2", Title: "Second"},
		{ID: "s3", Title: "Third"},
	}
}

// Autoplay is off unless asked for. Content that moves on its own is unreadable
// for anyone who reads slowly and steals attention from the rest of the page.
func TestAutoplayIsOffByDefault(t *testing.T) {
	html := renderComponent(t, Carousel(CarouselOpts{ID: "c", Label: "News", Slides: slides()}))

	assert.Contains(t, html, `data-carousel-autoplay="false"`)
}

// A sub-second rotation is unreadable, so a caller asking for one gets the floor
// rather than the number they typed.
func TestAutoplayIntervalHasAFloor(t *testing.T) {
	html := renderComponent(t, Carousel(CarouselOpts{
		ID: "c", Label: "News", Slides: slides(), Autoplay: true, IntervalMS: 200,
	}))

	assert.Contains(t, html, `data-carousel-autoplay="true"`)
	assert.Contains(t, html, `data-carousel-interval="4000"`)
	assert.NotContains(t, html, `data-carousel-interval="200"`)
}

// The scroller needs a tab stop, or the slides are reachable by pointer only.
func TestScrollerIsKeyboardReachable(t *testing.T) {
	html := renderComponent(t, Carousel(CarouselOpts{ID: "c", Label: "News", Slides: slides()}))

	assert.Contains(t, html, `tabindex="0"`)
	// Scroll snap, not a slideshow: every slide stays in the document, so none
	// is hidden from a screen reader or from find-in-page.
	assert.Contains(t, html, "snap-x")
	assert.Equal(t, 3, strings.Count(html, "data-carousel-slide="))
}

// The dots are anchors to real slides, so they work with scripting disabled.
// Buttons a controller has to wire up would be dead controls without it.
func TestDotsAreRealLinks(t *testing.T) {
	html := renderComponent(t, CarouselDots(CarouselDotsOpts{
		Slides: []string{"s1", "s2", "s3"}, Active: 1, Label: "News",
	}))

	assert.Contains(t, html, `href="#s1"`)
	assert.Contains(t, html, `href="#s3"`)
	assert.NotContains(t, html, "<button")
}

// A row of unlabelled dots is a set of controls a screen reader cannot tell
// apart, and the filled circle reports nothing.
func TestEachDotStatesItsPosition(t *testing.T) {
	html := renderComponent(t, CarouselDots(CarouselDotsOpts{
		Slides: []string{"s1", "s2"}, Active: 2, Label: "News",
	}))

	assert.Contains(t, html, "Slide 1 of 2")
	assert.Contains(t, html, "Slide 2 of 2")
	require.Equal(t, 1, strings.Count(html, `aria-current="true"`))
}

// A single slide is not a carousel: navigation for one destination is noise.
func TestOneSlideRendersNoDots(t *testing.T) {
	html := renderComponent(t, Carousel(CarouselOpts{
		ID: "c", Label: "News", Slides: []SlideData{{ID: "s1", Title: "Only"}},
	}))

	assert.NotContains(t, html, "data-carousel-dot")
	// And no position readout, because "1 of 1" tells the reader nothing.
	assert.NotContains(t, html, "1 of 1")
}

// The position is text. A scrollbar reports progress only to someone who can
// see it.
func TestSlideStatesItsPosition(t *testing.T) {
	html := renderComponent(t, Slide(SlideOpts{
		Slide: SlideData{ID: "s2", Title: "Second"}, Index: 2, Total: 3,
	}))

	assert.Contains(t, html, "2 of 3")
}

// An image with no description is invisible to a screen reader.
func TestSlideImageCarriesItsAlt(t *testing.T) {
	html := renderComponent(t, Slide(SlideOpts{
		Slide: SlideData{ID: "s", Title: "T", Image: "/a.png", Alt: "A wiring diagram"},
		Index: 1, Total: 2,
	}))

	assert.Contains(t, html, `alt="A wiring diagram"`)
}

// CarouselDots links to each slide's id, and the controller finds slides by
// data-carousel-slide. Both have to name whichever id the slide actually
// carries: a slide that took its id from Attrs.ID while the marker still read
// the empty SlideData.ID would be unreachable from the dots and invisible to
// the controller.
func TestSlideMarkerFollowsWhicheverIDTheCallerSupplied(t *testing.T) {
	declared := renderComponent(t, Slide(SlideOpts{
		Slide: SlideData{ID: "slide-2", Title: "Second"}, Index: 2, Total: 3,
		Attrs: Attrs{ID: "escape-hatch"},
	}))
	assert.Contains(t, declared, `id="slide-2"`, "SlideData.ID must outrank the generic Attrs.ID")
	assert.Contains(t, declared, `data-carousel-slide="slide-2"`)
	assert.NotContains(t, declared, "escape-hatch",
		"Attrs.ID stayed beside the id the slide owns; two ids on one element is invalid HTML")

	fallback := renderComponent(t, Slide(SlideOpts{
		Slide: SlideData{Title: "Second"}, Index: 2, Total: 3, Attrs: Attrs{ID: "from-attrs"},
	}))
	assert.Contains(t, fallback, `id="from-attrs"`)
	assert.Contains(t, fallback, `data-carousel-slide="from-attrs"`)
}
