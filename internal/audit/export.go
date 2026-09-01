package audit

import "context"

type Entry struct {
	ID, OrgID, UserID, Action string
	Metadata                  map[string]any
}
type Exporter interface {
	Export(context.Context, Entry) error
}
type Logger interface {
	Log(context.Context, string, string, string, map[string]any) error
}
type FuncExporter func(context.Context, Entry) error

func (f FuncExporter) Export(c context.Context, e Entry) error {
	if f == nil {
		return nil
	}
	return f(c, e)
}
