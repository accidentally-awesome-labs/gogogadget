package dev

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/stretchr/testify/require"
)

func TestDevSenderContract(t *testing.T) {
	dir := t.TempDir()
	sender := NewDevSender(slog.New(slog.NewTextHandler(io.Discard, nil)), dir)
	msg := mail.Message{To: "contract@example.com", Subject: "Contract subject", HTML: "<p>Contract body</p>", Text: "Contract body"}
	require.NoError(t, sender.Send(context.Background(), msg))
	matches, err := filepath.Glob(filepath.Join(dir, "*.html"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	body, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	require.Contains(t, string(body), msg.HTML)
}

func TestDevSenderFilenameBoundary(t *testing.T) {
	dir := t.TempDir()
	sender := NewDevSender(slog.New(slog.NewTextHandler(io.Discard, nil)), dir)
	require.NoError(t, sender.Send(context.Background(), mail.Message{To: "../../etc/passwd", HTML: "body"}))
	matches, err := filepath.Glob(filepath.Join(dir, "*.html"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
}
