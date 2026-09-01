package posthog

import (
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/analytics"
)

type Module struct{ Value *analytics.PostHogCapturer }
type Deps struct{ APIKey, Host string }

func NewModule(ctx context.Context, _ any, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.APIKey == "" || d.Host == "" {
		return nil, fmt.Errorf("posthog: API key and host are required")
	}
	v, err := analytics.NewPostHog(d.APIKey, d.Host)
	if err != nil {
		return nil, err
	}
	return &Module{Value: v}, nil
}
func (m *Module) Stop(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return nil
	}
	done := make(chan struct{})
	go func() { m.Value.Close(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("posthog: capturer is required")
	}
	return nil
}
