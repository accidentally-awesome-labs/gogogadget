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
	"github.com/gogogadget/gogogadget/internal/ratelimit"
)

type Client interface {
	Allow(context.Context, string, int, time.Duration) (int, bool, error)
}
type RESTClient struct {
	Endpoint, Token string
	HTTP            *http.Client
}

func (c *RESTClient) Allow(ctx context.Context, key string, limit int, window time.Duration) (int, bool, error) {
	if c == nil || c.Endpoint == "" || c.Token == "" {
		return 0, false, fmt.Errorf("rate limit redis: endpoint and token are required")
	}
	cmd := [][]string{{"INCR", key}, {"EXPIRE", key, fmt.Sprint(int(window.Seconds()))}}
	b, _ := json.Marshal(cmd)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(b))
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	cl := c.HTTP
	if cl == nil {
		cl = http.DefaultClient
	}
	resp, err := cl.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return 0, false, fmt.Errorf("rate limit redis: %s", resp.Status)
	}
	var out struct {
		Result []any `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, false, err
	}
	if len(out.Result) == 0 {
		return 0, false, fmt.Errorf("rate limit redis: missing result")
	}
	var used int
	if n, ok := out.Result[0].(float64); ok {
		used = int(n)
	}
	return limit - used, used <= limit, nil
}

type Limiter struct {
	client Client
	limit  int
	window time.Duration
}

func New(client Client, perMinute int) (*Limiter, error) {
	if client == nil {
		return nil, fmt.Errorf("redis rate limit: client is required")
	}
	if perMinute <= 0 {
		perMinute = 100
	}
	return &Limiter{client: client, limit: perMinute, window: time.Minute}, nil
}

var _ ratelimit.Limiter = (*Limiter)(nil)

func (l *Limiter) Allow(ctx context.Context, key string) (ratelimit.Decision, error) {
	if l == nil || l.client == nil {
		return ratelimit.Decision{}, fmt.Errorf("rate limit redis: client unavailable")
	}
	rem, ok, err := l.client.Allow(ctx, key, l.limit, l.window)
	if err != nil {
		return ratelimit.Decision{Allowed: false, Limit: l.limit}, err
	}
	d := ratelimit.Decision{Allowed: ok, Limit: l.limit, Remaining: rem}
	if !ok {
		d.RetryAfter = time.Second
	}
	return d, nil
}
func (l *Limiter) Health(ctx context.Context) error {
	if l == nil || l.client == nil {
		return fmt.Errorf("rate limit redis: client unavailable")
	}
	return ctx.Err()
}

type Deps struct {
	Client          Client
	Endpoint, Token string
	Limit           int
}
type Module struct{ Value *Limiter }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Client == nil {
		if d.Endpoint == "" && h != nil {
			d.Endpoint = h.Env("RATE_LIMIT_REDIS_URL")
		}
		if d.Token == "" && h != nil {
			d.Token = h.Env("RATE_LIMIT_REDIS_TOKEN")
		}
		if d.Endpoint != "" && d.Token != "" {
			d.Client = &RESTClient{Endpoint: strings.TrimRight(d.Endpoint, "/"), Token: d.Token}
		}
	}
	v, err := New(d.Client, d.Limit)
	if err != nil {
		return nil, err
	}
	return &Module{Value: v}, nil
}
func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("rate limit redis: limiter is required")
	}
	return m.Value.Health(ctx)
}
