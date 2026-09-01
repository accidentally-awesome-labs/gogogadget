package usage

import "context"

type Recorder interface {
	Record(context.Context, string, string, int64, string, map[string]any) error
}
type FuncRecorder func(context.Context, string, string, int64, string, map[string]any) error

func (f FuncRecorder) Record(c context.Context, o, n string, v int64, e string, m map[string]any) error {
	if f == nil {
		return nil
	}
	return f(c, o, n, v, e, m)
}
