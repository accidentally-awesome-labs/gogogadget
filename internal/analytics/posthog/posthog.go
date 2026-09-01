// Package posthog contains the PostHog SDK adapter. The analytics seam itself
// has no provider SDK dependency.
package posthog

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/analytics"
	"github.com/gogogadget/gogogadget/internal/apphost"
	posthoggo "github.com/posthog/posthog-go"
)

type Capturer struct{ client posthoggo.Client }

func New(apiKey, host string) (*Capturer, error) {
	if apiKey == "" || host == "" {
		return nil, fmt.Errorf("posthog: API key and host are required")
	}
	client, err := posthoggo.NewWithConfig(apiKey, posthoggo.Config{Endpoint: host})
	if err != nil {
		return nil, err
	}
	return &Capturer{client: client}, nil
}
func (c *Capturer) Capture(userID, event string, props map[string]any) {
	if c == nil || c.client == nil {
		return
	}
	p := posthoggo.NewProperties()
	for k, v := range props {
		p.Set(k, v)
	}
	_ = c.client.Enqueue(posthoggo.Capture{DistinctId: userID, Event: event, Properties: p})
}
func (c *Capturer) Close() {
	if c != nil && c.client != nil {
		_ = c.client.Close()
	}
}

var _ analytics.Capturer = (*Capturer)(nil)
var _ analytics.BufferingCapturer = (*Capturer)(nil)

type Module struct{ Value *Capturer }
type Deps struct{ APIKey, Host string }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if h != nil {
		if d.APIKey == "" {
			d.APIKey = h.Env("POSTHOG_API_KEY")
		}
		if d.Host == "" {
			d.Host = h.Env("POSTHOG_HOST")
		}
	}
	v, err := New(d.APIKey, d.Host)
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
	return ctx.Err()
}
