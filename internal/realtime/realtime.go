// Package realtime is the publish/subscribe seam used by SSE transports.
package realtime

import (
	"context"
	"errors"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"sync"
)

var ErrClosed = errors.New("realtime subscription closed")

type Broker interface {
	Publish(context.Context, string, []byte) error
	Subscribe(context.Context, string) (Subscription, error)
}
type Subscription interface {
	Next(context.Context) ([]byte, error)
	Close() error
}
type Memory struct {
	mu     sync.RWMutex
	topics map[string]map[*subscription]struct{}
}
type subscription struct {
	ctx    context.Context
	cancel context.CancelFunc
	ch     chan []byte
	once   sync.Once
}

func NewMemory() *Memory { return &Memory{topics: make(map[string]map[*subscription]struct{})} }
func (m *Memory) Publish(ctx context.Context, topic string, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.RLock()
	subs := m.topics[topic]
	for s := range subs {
		select {
		case s.ch <- append([]byte(nil), payload...):
		default:
		}
	}
	m.mu.RUnlock()
	return nil
}
func (m *Memory) Subscribe(ctx context.Context, topic string) (Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, cancel := context.WithCancel(ctx)
	s := &subscription{ctx: c, cancel: cancel, ch: make(chan []byte, 16)}
	m.mu.Lock()
	if m.topics[topic] == nil {
		m.topics[topic] = make(map[*subscription]struct{})
	}
	m.topics[topic][s] = struct{}{}
	m.mu.Unlock()
	return &memorySubscription{parent: m, topic: topic, s: s}, nil
}

type memorySubscription struct {
	parent *Memory
	topic  string
	s      *subscription
}

func (x *memorySubscription) Next(ctx context.Context) ([]byte, error) {
	select {
	case p := <-x.s.ch:
		return p, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-x.s.ctx.Done():
		return nil, ErrClosed
	}
}
func (x *memorySubscription) Close() error {
	x.s.once.Do(func() { x.s.cancel(); x.parent.mu.Lock(); delete(x.parent.topics[x.topic], x.s); x.parent.mu.Unlock() })
	return nil
}

func (m *Memory) Health(ctx context.Context) error { return ctx.Err() }

type Deps struct{}
type Module struct{ Value *Memory }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: NewMemory()}, ctx.Err()
}
