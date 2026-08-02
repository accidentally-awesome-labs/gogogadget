package web

import "github.com/jackc/pgx/v5/pgtype"

func sqlcText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
