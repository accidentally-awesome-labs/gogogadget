package webhooks

import "context"

type Emitter interface {
	Emit(context.Context, string, string, any) error
}
type FuncEmitter func(context.Context, string, string, any) error

func (f FuncEmitter) Emit(c context.Context, o, t string, d any) error {
	if f == nil {
		return nil
	}
	return f(c, o, t, d)
}
