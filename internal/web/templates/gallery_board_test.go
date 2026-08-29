package templates

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func columnByID(t *testing.T, card, to, id string) (int, []string) {
	t.Helper()
	for _, column := range galleryBoardWithMove(card, to) {
		if column.ID != id {
			continue
		}
		ids := make([]string, 0, len(column.Cards))
		for _, c := range column.Cards {
			ids = append(ids, c.ID)
		}
		return column.Count, ids
	}
	require.FailNowf(t, "missing column", "no column %q", id)
	return 0, nil
}

// A move removes the card from its old column as well as adding it to the new
// one. Adding without removing duplicates the card, which reads as two pieces of
// work where there is one.
func TestMoveRelocatesRatherThanCopies(t *testing.T) {
	_, backlog := columnByID(t, "card-catalog", "done", "backlog")
	_, done := columnByID(t, "card-catalog", "done", "done")

	assert.NotContains(t, backlog, "card-catalog")
	assert.Contains(t, done, "card-catalog")
}

// The count is derived from the cards placed, so the readout cannot disagree
// with the board - including after a move.
func TestCountsFollowTheCards(t *testing.T) {
	count, ids := columnByID(t, "card-catalog", "done", "done")

	assert.Equal(t, len(ids), count)
	assert.Equal(t, 1, count)
}

// An unknown destination is refused. Creating the column on demand would put the
// card somewhere nobody can navigate to.
func TestUnknownDestinationLeavesTheBoardAlone(t *testing.T) {
	_, backlog := columnByID(t, "card-catalog", "nowhere", "backlog")

	assert.Contains(t, backlog, "card-catalog")
	assert.False(t, GalleryBoardHasColumn("nowhere"))
	assert.True(t, GalleryBoardHasColumn("done"))
}

// No move requested must leave every card where it started, or simply rendering
// the board would shuffle it.
func TestNoMoveLeavesEveryCardInPlace(t *testing.T) {
	_, backlog := columnByID(t, "", "", "backlog")
	_, doing := columnByID(t, "", "", "doing")

	assert.Equal(t, []string{"card-catalog", "card-docs"}, backlog)
	assert.Equal(t, []string{"card-grid"}, doing)
}

// The demo holds no state: a move applies to a fresh copy, so two readers never
// see each other's drags.
func TestBoardIsStateless(t *testing.T) {
	_, moved := columnByID(t, "card-catalog", "done", "done")
	require.Contains(t, moved, "card-catalog")

	_, after := columnByID(t, "", "", "done")
	assert.Empty(t, after, "a later render must not inherit an earlier move")
}
