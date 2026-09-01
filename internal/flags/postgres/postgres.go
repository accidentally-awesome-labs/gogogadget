// Package postgres adapts the existing transactional feature flag queries.
package postgres

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/cache"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/flags"
)

type Service struct {
	DB    *sqlc.Queries
	Cache cache.Store
}

func New(db *sqlc.Queries, c cache.Store) *Service {
	return &Service{DB: db, Cache: c}
}

var _ flags.Service = (*Service)(nil)

func (s *Service) impl() (*flags.DBEvaluator, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("postgres flags: queries are required")
	}
	return flags.NewDBEvaluatorWithCache(s.DB, 0, s.Cache), nil
}
func (s *Service) Enabled(c context.Context, o, k string) bool {
	e, err := s.impl()
	if err != nil {
		slog.ErrorContext(c, "feature flags backend unavailable", "error", err)
		return false
	}
	return e.Enabled(c, o, k)
}
func (s *Service) List(c context.Context) ([]flags.Flag, error) {
	e, err := s.impl()
	if err != nil {
		return nil, err
	}
	return e.List(c)
}
func (s *Service) Upsert(c context.Context, f flags.Flag) error {
	e, err := s.impl()
	if err != nil {
		return err
	}
	return e.Upsert(c, f)
}
func (s *Service) Delete(c context.Context, k string) error {
	e, err := s.impl()
	if err != nil {
		return err
	}
	return e.Delete(c, k)
}
func (s *Service) ListOverrides(c context.Context, k string) ([]flags.Override, error) {
	e, err := s.impl()
	if err != nil {
		return nil, err
	}
	return e.ListOverrides(c, k)
}
func (s *Service) SetOverride(c context.Context, k, o string, v bool) error {
	e, err := s.impl()
	if err != nil {
		return err
	}
	return e.SetOverride(c, k, o, v)
}
func (s *Service) DeleteOverride(c context.Context, k, o string) error {
	e, err := s.impl()
	if err != nil {
		return err
	}
	return e.DeleteOverride(c, k, o)
}

func (s *Service) Health(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("postgres flags: queries are required")
	}
	return nil
}

type Deps struct {
	Queries *sqlc.Queries
	Cache   cache.Store
}
type Module struct{ Value *Service }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Queries == nil {
		return nil, fmt.Errorf("postgres flags: queries are required")
	}
	return &Module{Value: New(d.Queries, d.Cache)}, nil
}

func (m *Module) Health(ctx context.Context) error { return m.Value.Health(ctx) }
