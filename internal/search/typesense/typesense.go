// Package typesense implements the managed Typesense search target using its
// HTTP protocol; the search seam remains independent of a vendor SDK.
package typesense

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gogogadget/gogogadget/internal/search"
)

type Index struct {
	Endpoint, APIKey string
	Client           *http.Client
}

func New(endpoint, key string) *Index {
	return &Index{Endpoint: strings.TrimRight(endpoint, "/"), APIKey: key, Client: &http.Client{}}
}
func (i *Index) request(ctx context.Context, method, path string, body any, out any) error {
	if i == nil || i.Endpoint == "" {
		return fmt.Errorf("typesense: endpoint is required")
	}
	client := i.Client
	if client == nil {
		return fmt.Errorf("typesense: HTTP client is required")
	}
	var r *http.Request
	var err error
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r, err = http.NewRequestWithContext(ctx, http.MethodPost, i.Endpoint+path, bytes.NewReader(b))
		if err != nil {
			return err
		}
		r.Header.Set("Content-Type", "application/json")
	} else {
		r, err = http.NewRequestWithContext(ctx, method, i.Endpoint+path, nil)
		if err != nil {
			return err
		}
	}
	r.Header.Set("X-TYPESENSE-API-KEY", i.APIKey)
	resp, err := client.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("typesense: %s", resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
func (i *Index) Upsert(ctx context.Context, d search.Document) error {
	if d.TenantID == "" || d.Collection == "" || d.ID == "" {
		return fmt.Errorf("typesense: tenant, collection, and id are required")
	}
	payload := map[string]any{"id": d.ID, "tenant_id": d.TenantID, "collection": d.Collection, "text": d.Text, "fields": d.Fields}
	return i.request(ctx, http.MethodPost, "/collections/"+url.PathEscape(d.Collection)+"/documents?action=upsert", payload, nil)
}
func (i *Index) Delete(ctx context.Context, tenantID, collection, id string) error {
	if tenantID == "" || collection == "" || id == "" {
		return fmt.Errorf("typesense: tenant, collection, and id are required")
	}
	// Typesense's document DELETE endpoint identifies a row by ID; filter_by is
	// not a delete authorization boundary. Read first and refuse to delete a
	// row owned by another tenant.
	var doc struct {
		TenantID string `json:"tenant_id"`
	}
	if err := i.request(ctx, http.MethodGet, "/collections/"+url.PathEscape(collection)+"/documents/"+url.PathEscape(id), nil, &doc); err != nil {
		return err
	}
	if doc.TenantID != tenantID {
		return fmt.Errorf("typesense: document belongs to another tenant")
	}
	return i.request(ctx, http.MethodDelete, "/collections/"+url.PathEscape(collection)+"/documents/"+url.PathEscape(id), nil, nil)
}
func (i *Index) Query(ctx context.Context, q search.Query) (search.Result, error) {
	if q.TenantID == "" || q.Collection == "" {
		return search.Result{}, fmt.Errorf("typesense: tenant and collection are required")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	values := url.Values{
		"q":         []string{q.Text},
		"query_by":  []string{"text"},
		"per_page":  []string{strconv.Itoa(limit)},
		"filter_by": []string{"tenant_id:=" + q.TenantID},
	}
	if q.Cursor != "" {
		values.Set("page", q.Cursor)
	}
	for key, value := range q.Filters {
		if key == "" || strings.ContainsAny(key, "\r\n") {
			return search.Result{}, fmt.Errorf("typesense: invalid filter key %q", key)
		}
		values.Set("filter_by", values.Get("filter_by")+" && "+key+":="+value)
	}
	path := "/collections/" + url.PathEscape(q.Collection) + "/documents/search?" + values.Encode()
	var raw struct {
		Hits []struct {
			Document struct {
				ID       string            `json:"id"`
				TenantID string            `json:"tenant_id"`
				Fields   map[string]string `json:"fields"`
			} `json:"document"`
			TextMatch int `json:"text_match"`
		} `json:"hits"`
		Next string `json:"next_page"`
	}
	if err := i.request(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return search.Result{}, err
	}
	out := search.Result{NextCursor: raw.Next}
	for _, h := range raw.Hits {
		// Treat the remote response as untrusted: an incorrectly configured
		// collection must never leak another tenant through this seam.
		if h.Document.TenantID != q.TenantID {
			continue
		}
		out.Hits = append(out.Hits, search.Hit{ID: h.Document.ID, Score: float64(h.TextMatch), Fields: h.Document.Fields})
	}
	return out, nil
}

func (i *Index) Health(ctx context.Context) error {
	if i == nil || i.Endpoint == "" {
		return fmt.Errorf("typesense: endpoint is required")
	}
	if i.Client == nil {
		return fmt.Errorf("typesense: HTTP client is required")
	}
	return nil
}
