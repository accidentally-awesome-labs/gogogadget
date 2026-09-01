package flags

import (
	"context"
	"errors"
)

var ErrReadOnly = errors.New("feature flags provider is read-only")

type Flag struct {
	Key, Description string
	Enabled          bool
	Rollout          int
}
type Override struct {
	OrgID   string
	Enabled bool
}
type Service interface {
	Enabled(context.Context, string, string) bool
	List(context.Context) ([]Flag, error)
	Upsert(context.Context, Flag) error
	Delete(context.Context, string) error
	ListOverrides(context.Context, string) ([]Override, error)
	SetOverride(context.Context, string, string, bool) error
	DeleteOverride(context.Context, string, string) error
}
