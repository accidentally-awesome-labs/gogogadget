// cmd/server is wiring only: config → db → migrator → services → web.Server →
// graceful shutdown. All behavior lives in internal packages.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	contentfs "github.com/gogogadget/gogogadget/content"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/jobs"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/gogogadget/gogogadget/internal/web"
)

// version is stamped at build time: -ldflags "-X main.version=$VERSION".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg)
	log.Info("starting", "env", cfg.Env, "version", version, "port", cfg.Port)

	billing.SetPolarProductIDs(cfg.PolarProductPro, cfg.PolarProductTeam)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}

	blog, err := content.LoadBlog(contentfs.FS, cfg.Production())
	if err != nil {
		return fmt.Errorf("load blog: %w", err)
	}
	docs, err := content.LoadDocs(contentfs.FS, cfg.Production())
	if err != nil {
		return fmt.Errorf("load docs: %w", err)
	}

	// Identity: FakeVerifier powers zero-account dev/e2e; ClerkVerifier is the
	// only production path. Both satisfy the same seam.
	var verifier identity.Verifier
	var fetcher identity.UserFetcher
	switch {
	case cfg.DevAuthBypass:
		verifier = identity.FakeVerifier{}
		fetcher = identity.DevUserFetcher{}
		log.Warn("DEV_AUTH_BYPASS enabled — synthetic e2e: tokens accepted")
	case cfg.ClerkConfigured():
		verifier = identity.NewClerkVerifier(cfg.ClerkSecretKey)
		fetcher = identity.NewClerkUserFetcher(cfg.ClerkSecretKey)
	default:
		log.Warn("clerk not configured — /app routes will 503")
	}

	q := sqlc.New(pool)
	srv := web.NewServer(web.Deps{
		Config: cfg, Log: log, DB: pool, Queries: q, Version: version,
		Blog: blog, Docs: docs,
		Verifier: verifier, Fetcher: fetcher,
		Billing: nil, // Polar client lands in the billing step
	})

	// Mail: Resend when configured, DevSender (log + tmp/emails/) otherwise.
	var sender mail.Sender
	if cfg.ResendConfigured() {
		sender = mail.NewResendSender(cfg.ResendAPIKey, cfg.EmailFrom)
		log.Info("mail: resend", "from", cfg.EmailFrom)
	} else {
		sender = mail.NewDevSender(log, "tmp/emails")
		log.Info("mail: dev sender (tmp/emails)")
	}

	// Background jobs worker (SKIP LOCKED claim; stops on shutdown signal).
	worker := jobs.NewWorker(sqlc.New(pool), sender, log)
	go worker.Run(ctx)

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", "http://localhost:"+fmt.Sprint(cfg.Port))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

func newLogger(cfg config.Config) *slog.Logger {
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.Production() {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
