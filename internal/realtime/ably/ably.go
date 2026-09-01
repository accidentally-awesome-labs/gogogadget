// Package ably adapts the Ably REST publish endpoint while subscriptions are
// supplied by the host's SSE/WebSocket bridge.
package ably

import (
	"bytes"
	"context"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/realtime"
	"net/http"
	"net/url"
)

type Subscriber interface {
	Subscribe(context.Context, string) (realtime.Subscription, error)
}
type Broker struct {
	Endpoint, APIKey string
	Client           *http.Client
	Subscriber       Subscriber
}

func New(endpoint, key string, s Subscriber) *Broker {
	return &Broker{Endpoint: endpoint, APIKey: key, Client: http.DefaultClient, Subscriber: s}
}
func (b *Broker) Publish(ctx context.Context, topic string, payload []byte) error {
	if b == nil || b.Endpoint == "" {
		return fmt.Errorf("ably: endpoint is required")
	}
	u := b.Endpoint + "/channels/" + url.PathEscape(topic) + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Basic "+b.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ably: %s", resp.Status)
	}
	return nil
}
func (b *Broker) Subscribe(ctx context.Context, t string) (realtime.Subscription, error) {
	if b.Subscriber == nil {
		return nil, fmt.Errorf("ably: subscriber is required")
	}
	return b.Subscriber.Subscribe(ctx, t)
}

var _ realtime.Broker = (*Broker)(nil)

func (b *Broker) Health(ctx context.Context) error {
	if b == nil || b.Endpoint == "" {
		return fmt.Errorf("ably: endpoint is required")
	}
	return nil
}

type Deps struct{}
type Module struct{ Value *Broker }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: New(h.Env("ABLY_ENDPOINT"), h.Env("ABLY_API_KEY"), nil)}, ctx.Err()
}

func (m *Module) Health(ctx context.Context) error { return m.Value.Health(ctx) }
