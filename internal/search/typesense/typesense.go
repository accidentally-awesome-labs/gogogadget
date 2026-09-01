// Package typesense implements the managed Typesense search target using its
// HTTP protocol; the search seam remains independent of a vendor SDK.
package typesense

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/search"
	"net/http"
	"net/url"
	"strconv"
)

type Index struct {
	Endpoint, APIKey string
	Client           *http.Client
}

func New(endpoint, key string) *Index {
	return &Index{Endpoint: endpoint, APIKey: key, Client: http.DefaultClient}
}
func (i *Index) request(ctx context.Context, method, path string, body any, out any) error {
	if i == nil || i.Endpoint == "" {
		return fmt.Errorf("typesense: endpoint is required")
	}
	var r *http.Request
	var err error
	if body != nil {
		b, _ := json.Marshal(body)
		r, err = http.NewRequestWithContext(ctx, method, i.Endpoint+path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r, err = http.NewRequestWithContext(ctx, method, i.Endpoint+path, nil)
	}
	if err != nil {
		return err
	}
	r.Header.Set("X-TYPESENSE-API-KEY", i.APIKey)
	resp, err := i.Client.Do(r)
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
	payload := map[string]any{"id": d.ID, "tenant_id": d.TenantID, "collection": d.Collection, "text": d.Text, "fields": d.Fields}
	return i.request(ctx, http.MethodPost, "/collections/"+url.PathEscape(d.Collection)+"/documents?action=upsert", payload, nil)
}
func (i *Index) Delete(ctx context.Context, t, c, id string) error {
	if t == "" {
		return fmt.Errorf("typesense: tenant is required")
	}
	return i.request(ctx, http.MethodDelete, "/collections/"+url.PathEscape(c)+"/documents/"+url.PathEscape(id)+"?filter_by=tenant_id%3D"+url.QueryEscape(t), nil, nil)
}
func (i *Index) Query(ctx context.Context, q search.Query) (search.Result, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	path := "/collections/" + url.PathEscape(q.Collection) + "/documents/search?q=" + url.QueryEscape(q.Text) + "&per_page=" + strconv.Itoa(limit) + "&filter_by=tenant_id%3D" + url.QueryEscape(q.TenantID)
	if q.Cursor != "" {
		path += "&page=" + url.QueryEscape(q.Cursor)
	}
	var raw struct {
		Hits []struct {
			Document struct {
				ID     string            `json:"id"`
				Fields map[string]string `json:"fields"`
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
		out.Hits = append(out.Hits, search.Hit{ID: h.Document.ID, Score: float64(h.TextMatch), Fields: h.Document.Fields})
	}
	return out, nil
}

func (i *Index) Health(ctx context.Context) error {
	if i == nil || i.Endpoint == "" {
		return fmt.Errorf("typesense: endpoint is required")
	}
	return nil
}
