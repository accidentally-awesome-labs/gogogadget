package postgres

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/search"
)

// DrainJob is the durable worker boundary for search indexing. Product writes
// enqueue intents transactionally; this job is the only path that performs
// provider I/O and retries failed delivery through the outbox.
type DrainJob struct {
	Outbox    *search.Outbox
	BatchSize int
}

func (j DrainJob) Run(ctx context.Context) error {
	if j.Outbox == nil {
		return context.Canceled
	}
	return j.Outbox.Drain(ctx, j.BatchSize)
}
