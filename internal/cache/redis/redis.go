// Package redis adapts a Redis-compatible cache client. The interface keeps
// the provider seam free of Valkey/Upstash SDKs.
package redis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/cache"
)

type Client interface {
	Get(context.Context, string) ([]byte, bool, error)
	Set(context.Context, string, []byte, time.Duration) error
	Delete(context.Context, string) error
}

// RESTClient speaks the Upstash REST command protocol, which is also usable by
// Valkey-compatible REST gateways and avoids a provider SDK in this adapter.
type RESTClient struct {
	Endpoint, Token string
	HTTP            *http.Client
}

func (c *RESTClient) command(ctx context.Context, args ...string) (any, error) {
	if c == nil || c.Endpoint == "" || c.Token == "" {
		return nil, fmt.Errorf("redis cache: endpoint and token are required")
	}
	b, _ := json.Marshal(map[string]any{"command": args})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	cl := c.HTTP
	if cl == nil {
		cl = http.DefaultClient
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("redis cache: %s", resp.Status)
	}
	var out struct {
		Result any `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Result, nil
}
func (c *RESTClient) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, err := c.command(ctx, "GET", key)
	if err != nil {
		return nil, false, err
	}
	if v == nil {
		return nil, false, nil
	}
	return []byte(fmt.Sprint(v)), true, nil
}
func (c *RESTClient) Set(ctx context.Context, key string, v []byte, ttl time.Duration) error {
	args := []string{"SET", key, string(v)}
	if ttl > 0 {
		args = append(args, "PX", fmt.Sprint(ttl.Milliseconds()))
	}
	_, err := c.command(ctx, args...)
	return err
}
func (c *RESTClient) Delete(ctx context.Context, key string) error {
	_, err := c.command(ctx, "DEL", key)
	return err
}

type Store struct{ client Client }

func New(client Client) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("redis cache: client is required")
	}
	return &Store{client: client}, nil
}

var _ cache.Store = (*Store)(nil)

func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if s == nil || s.client == nil {
		return nil, false, fmt.Errorf("redis cache: client unavailable")
	}
	return s.client.Get(ctx, key)
}
func (s *Store) Set(ctx context.Context, key string, v []byte, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis cache: client unavailable")
	}
	return s.client.Set(ctx, key, v, ttl)
}
func (s *Store) Delete(ctx context.Context, key string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis cache: client unavailable")
	}
	return s.client.Delete(ctx, key)
}
func (s *Store) Health(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis cache: client unavailable")
	}
	return ctx.Err()
}

type Deps struct {
	Client          Client
	Endpoint, Token string
}
type Module struct{ Value *Store }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Client == nil {
		if d.Endpoint == "" && h != nil {
			d.Endpoint = h.Env("CACHE_REDIS_URL")
		}
		if d.Token == "" && h != nil {
			d.Token = h.Env("CACHE_REDIS_TOKEN")
		}
		if d.Endpoint != "" && d.Token != "" {
			d.Client = &RESTClient{Endpoint: strings.TrimRight(d.Endpoint, "/"), Token: d.Token}
		}
	}
	v, err := New(d.Client)
	if err != nil {
		return nil, err
	}
	return &Module{Value: v}, nil
}
func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("redis cache: store is required")
	}
	return m.Value.Health(ctx)
}
