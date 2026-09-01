package web

import (
	"context"
	identitysession "github.com/gogogadget/gogogadget/internal/identity/session"
)

type testSessionLoader struct{}

func (testSessionLoader) Load(context.Context, string) (identitysession.Session, error) {
	return identitysession.Session{}, context.Canceled
}
