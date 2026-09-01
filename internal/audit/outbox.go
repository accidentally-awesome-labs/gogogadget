package audit

import "context"

type Outbox interface {
	Enqueue(context.Context, Entry) error
}
type ExportJob struct {
	Queue    Outbox
	Exporter Exporter
}

func (j ExportJob) Enqueue(ctx context.Context, entry Entry) error {
	if j.Queue == nil {
		return context.Canceled
	}
	return j.Queue.Enqueue(ctx, entry)
}
func (j ExportJob) Run(ctx context.Context, entries []Entry) error {
	if j.Exporter == nil {
		return context.Canceled
	}
	for _, entry := range entries {
		if err := j.Exporter.Export(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}
