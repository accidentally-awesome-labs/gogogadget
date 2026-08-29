package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// POST /dev/ui/kanban/move — the gallery board's move endpoint.
//
// The board demo needs a real destination. A demo that posts to the page itself
// destroys the page on the first drag, and a demo wired to a no-op button proves
// nothing: the point of the component is that a move is a server decision, so
// the only honest demo is one where the server actually decides.
//
// This is dev-only, registered under the same zero-account gate as the gallery.
// It holds no state: it re-renders the board with the card placed in the
// requested column, which is exactly the contract a real handler implements -
// and, by returning a board, it is also what reverts a move the server refused.
func (s *Server) handleDevKanbanMove(w http.ResponseWriter, r *http.Request) {
	card := r.FormValue("card")
	to := r.FormValue("to")

	// A fragment endpoint answering a full-page navigation leaves the browser on
	// a layout-less board: no shell, no stylesheet, no way back. With scripting
	// off the card's move buttons are ordinary form submits, so that is exactly
	// where a no-script move landed. Redirect to the page instead.
	//
	// The move is not carried into the redirect, and that is not an omission:
	// this endpoint holds no state, so there is nothing to carry. The scripted
	// path appears to move the card only because the response is swapped in
	// place - reload the gallery and the card is back in its original column
	// there too. A demo that faked persistence through a query parameter would
	// be claiming a durability the component's real callers have to provide
	// themselves.
	//
	// 303 and not 307: the follow-up must be a GET. A 307 would repeat the POST
	// on reload, and reloading is the first thing anyone does after landing
	// somewhere unexpected.
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, "/dev/gallery", http.StatusSeeOther)
		return
	}

	// An unknown destination is refused, and refusal re-renders the board
	// unchanged. That is the whole reason the response is authoritative: a
	// rejected move has to put the card back.
	if !templates.GalleryBoardHasColumn(to) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		templates.GalleryBoard("", "").Render(r.Context(), w)
		return
	}
	templates.GalleryBoard(card, to).Render(r.Context(), w)
}
