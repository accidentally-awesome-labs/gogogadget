// Package search is the tenant-scoped indexing and query seam.
package search

import "context"

type Index interface {
	Upsert(context.Context, Document) error
	Delete(context.Context, string, string, string) error
	Query(context.Context, Query) (Result, error)
}
type Document struct {
	TenantID, Collection, ID, Text string
	Fields                         map[string]string
}
type Query struct {
	TenantID, Collection, Text, Cursor string
	Filters                            map[string]string
	Limit                              int
}
type Result struct {
	Hits       []Hit
	NextCursor string
}
type Hit struct {
	ID     string
	Score  float64
	Fields map[string]string
}
