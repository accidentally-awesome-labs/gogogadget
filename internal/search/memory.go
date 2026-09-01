package search

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type Memory struct {
	mu   sync.RWMutex
	docs map[string]Document
}

func NewMemory() *Memory { return &Memory{docs: make(map[string]Document)} }
func (m *Memory) Upsert(ctx context.Context, d Document) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.TenantID == "" || d.Collection == "" || d.ID == "" {
		return fmt.Errorf("search: tenant, collection, and id are required")
	}
	m.mu.Lock()
	m.docs[d.TenantID+"\x00"+d.Collection+"\x00"+d.ID] = clone(d)
	m.mu.Unlock()
	return nil
}
func (m *Memory) Delete(ctx context.Context, t, c, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.docs, t+"\x00"+c+"\x00"+id)
	m.mu.Unlock()
	return nil
}
func (m *Memory) Query(ctx context.Context, q Query) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	var out Result
	needle := strings.ToLower(q.Text)
	for _, d := range m.docs {
		if d.TenantID != q.TenantID || d.Collection != q.Collection {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(d.Text), needle) {
			continue
		}
		ok := true
		for k, v := range q.Filters {
			if d.Fields[k] != v {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		out.Hits = append(out.Hits, Hit{ID: d.ID, Score: 1, Fields: cloneFields(d.Fields)})
		if len(out.Hits) >= limit {
			break
		}
	}
	return out, nil
}
func clone(d Document) Document { d.Fields = cloneFields(d.Fields); return d }
func cloneFields(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	n := make(map[string]string, len(m))
	for k, v := range m {
		n[k] = v
	}
	return n
}

func (m *Memory) Health(ctx context.Context) error { return ctx.Err() }
