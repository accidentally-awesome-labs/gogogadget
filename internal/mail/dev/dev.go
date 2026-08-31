// Package dev implements the zero-account filesystem mail adapter.
package dev

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/mail"
)

type Deps struct {
	Config *config.Config
}

type Module struct {
	Sender mail.Sender
}

func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("mail dev: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Module{Sender: NewDevSender(h.Log(), "tmp/emails")}, nil
}

// DevSender logs and writes rendered mail to disk so development and test need
// no account or network service.
type DevSender struct {
	log *slog.Logger
	dir string
}

func NewDevSender(log *slog.Logger, dir string) *DevSender {
	return &DevSender{log: log, dir: dir}
}

func (s *DevSender) Send(_ context.Context, msg mail.Message) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("%d-%s.html", time.Now().UnixNano(), sanitizeFilename(msg.To))
	path := filepath.Join(s.dir, name)
	if err := os.WriteFile(path, []byte(msg.HTML), 0o644); err != nil {
		return err
	}
	s.log.Info("dev email", "to", msg.To, "subject", msg.Subject, "file", path)
	return nil
}

func sanitizeFilename(s string) string {
	mapped := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
	for strings.Contains(mapped, "..") {
		mapped = strings.ReplaceAll(mapped, "..", ".")
	}
	return mapped
}

var _ mail.Sender = (*DevSender)(nil)
