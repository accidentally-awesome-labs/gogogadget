package otlp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/audit"
	"net/http"
)

type Exporter struct {
	Endpoint string
	Client   *http.Client
}

func New(endpoint string) *Exporter { return &Exporter{Endpoint: endpoint, Client: http.DefaultClient} }
func (e *Exporter) Export(c context.Context, entry audit.Entry) error {
	if e == nil || e.Endpoint == "" {
		return fmt.Errorf("audit export: endpoint is required")
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(c, http.MethodPost, e.Endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("audit export: %s", resp.Status)
	}
	return nil
}

var _ audit.Exporter = (*Exporter)(nil)

func (e *Exporter) Health(ctx context.Context) error {
	if e == nil || e.Endpoint == "" {
		return fmt.Errorf("audit export: endpoint is required")
	}
	return nil
}

type Deps struct{}
type Module struct{ Value *Exporter }

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	return &Module{Value: New(h.Env("OTLP_AUDIT_EXPORT_URL"))}, ctx.Err()
}

func (m *Module) Health(ctx context.Context) error {
	if m == nil || m.Value == nil {
		return fmt.Errorf("audit export: exporter is required")
	}
	return m.Value.Health(ctx)
}
