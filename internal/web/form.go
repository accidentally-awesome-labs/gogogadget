package web

import (
	"net/http"

	form "github.com/go-playground/form/v4"
)

var formDecoder = form.NewDecoder()

// decodeForm parses the body and decodes it into dst.
func decodeForm(r *http.Request, dst any) error {
	if err := r.ParseForm(); err != nil {
		return err
	}
	return formDecoder.Decode(dst, r.Form)
}
