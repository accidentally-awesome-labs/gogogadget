package search

import (
	"context"
	"encoding/json"
	"time"
)

type OutboxEntry struct {
	TenantID, Collection, DocumentID, Operation string
	Payload                                     json.RawMessage
	Attempts                                    int
	AvailableAt, CreatedAt, UpdatedAt           time.Time
}
type OutboxStore interface {
	Enqueue(context.Context, OutboxEntry) error
	Claim(context.Context, int) ([]OutboxEntry, error)
	Ack(context.Context, OutboxEntry) error
	Backoff(context.Context, OutboxEntry, time.Time) error
}
type Outbox struct {
	Store OutboxStore
	Index Index
	Now   func() time.Time
}

func (o *Outbox) Enqueue(ctx context.Context, e OutboxEntry) error {
	if o == nil || o.Store == nil {
		return context.Canceled
	}
	return o.Store.Enqueue(ctx, e)
}
func (o *Outbox) Drain(ctx context.Context, limit int) error {
	if o == nil || o.Store == nil || o.Index == nil {
		return context.Canceled
	}
	rows, err := o.Store.Claim(ctx, limit)
	if err != nil {
		return err
	}
	for _, e := range rows {
		var d Document
		if json.Unmarshal(e.Payload, &d) != nil {
			_ = o.Store.Backoff(ctx, e, o.clock().Add(time.Minute))
			continue
		}
		var x error
		if e.Operation == "delete" {
			x = o.Index.Delete(ctx, e.TenantID, e.Collection, e.DocumentID)
		} else {
			x = o.Index.Upsert(ctx, d)
		}
		if x != nil {
			_ = o.Store.Backoff(ctx, e, o.clock().Add(backoff(e.Attempts)))
			continue
		}
		if err := o.Store.Ack(ctx, e); err != nil {
			return err
		}
	}
	return nil
}
func (o *Outbox) clock() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}
func backoff(a int) time.Duration {
	if a < 0 {
		a = 0
	}
	if a > 8 {
		a = 8
	}
	return time.Duration(1<<a) * time.Second
}
