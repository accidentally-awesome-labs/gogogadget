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

	"github.com/getsentry/sentry-go"
	contentfs "github.com/gogogadget/gogogadget/content"
	"github.com/gogogadget/gogogadget/internal/analytics"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/jobs"
	"github.com/gogogadget/gogogadget/internal/llm"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/gogogadget/gogogadget/internal/observability"
	"github.com/gogogadget/gogogadget/internal/storage"
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
	changelog, err := content.LoadChangelog(contentfs.FS)
	if err != nil {
		return fmt.Errorf("load changelog: %w", err)
	}

	// Identity: FakeVerifier powers zero-account dev/e2e; ClerkVerifier is the
	// only production path. Both satisfy the same seam.
	var verifier identity.Verifier
	var fetcher identity.UserFetcher
	var deleter identity.Deleter // nil = local-only account deletion
	switch {
	case cfg.DevAuthBypass:
		verifier = identity.FakeVerifier{}
		fetcher = identity.DevUserFetcher{}
		deleter = identity.DevDeleter{}
		log.Warn("DEV_AUTH_BYPASS enabled — synthetic e2e: tokens accepted")
	case cfg.ClerkConfigured():
		verifier = identity.NewClerkVerifier(cfg.ClerkSecretKey)
		fetcher = identity.NewClerkUserFetcher(cfg.ClerkSecretKey)
		deleter = identity.NewClerkDeleter(cfg.ClerkSecretKey)
	default:
		log.Warn("clerk not configured — /app routes will 503")
	}

	var polarClient billing.Client
	if cfg.PolarConfigured() {
		polarClient = billing.NewPolarClient(cfg.PolarAccessToken, cfg.PolarServer)
		log.Info("billing: polar", "server", cfg.PolarServer)
	} else {
		log.Warn("polar not configured — billing routes will 503")
	}

	// Storage: R2 when configured; DevStore (tmp/uploads) otherwise.
	var fileStore storage.Store
	if cfg.StorageConfigured() {
		r2, err := storage.NewR2Store(ctx, cfg.StorageR2AccountID, cfg.StorageR2AccessKeyID, cfg.StorageR2SecretAccessKey, cfg.StorageR2Bucket, cfg.StorageR2Endpoint)
		if err != nil {
			return fmt.Errorf("r2 storage init: %w", err)
		}
		fileStore = r2
		log.Info("storage: r2", "bucket", cfg.StorageR2Bucket)
	} else {
		fileStore = storage.NewDevStore("tmp/uploads")
		log.Info("storage: dev store (tmp/uploads)")
	}

	// LLM: any OpenAI-compatible API; unconfigured → AI routes 503.
	var completer llm.Completer
	if cfg.LLMConfigured() {
		completer = llm.NewOpenAICompat(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
		log.Info("llm: openai-compatible", "model", cfg.LLMModel, "base", cfg.LLMBaseURL)
	} else {
		log.Warn("llm not configured — AI routes will 503")
	}

	// Observability: both env-gated. The reporter seam wraps Sentry when a DSN
	// is set; Noop otherwise.
	if cfg.SentryEnabled() {
		if err := sentry.Init(sentry.ClientOptions{Dsn: cfg.SentryDSN, Environment: cfg.Env}); err != nil {
			return fmt.Errorf("sentry init: %w", err)
		}
		defer sentry.Flush(2 * time.Second)
		log.Info("sentry: enabled")
	}
	var reporter observability.Reporter = observability.NoopReporter{}
	if cfg.SentryEnabled() {
		reporter = observability.NewSentryReporter()
	}
	var capturer analytics.Capturer = analytics.NoopCapturer{}
	if cfg.PostHogEnabled() {
		ph, err := analytics.NewPostHog(cfg.PostHogAPIKey, cfg.PostHogHost)
		if err != nil {
			return fmt.Errorf("posthog init: %w", err)
		}
		defer ph.Close()
		capturer = ph
		log.Info("posthog: enabled")
	}

	q := sqlc.New(pool)
	srv := web.NewServer(web.Deps{
		Config: cfg, Log: log, DB: pool, Queries: q, Version: version,
		Blog: blog, Docs: docs, Changelog: changelog,
		Verifier: verifier, Fetcher: fetcher, IdentityDeleter: deleter,
		Billing: polarClient, Analytics: capturer,
		Storage:  fileStore,
		LLM:      completer,
		Reporter: reporter,
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
	worker.Billing = polarClient // nil-safe: usage.flush no-ops when unconfigured
	worker.AuditRetentionDays = cfg.AuditRetentionDays
	worker.Storage = fileStore // export jobs write through the same seam
	worker.AppURL = cfg.AppURL // digest emails are rendered by the worker
	if cfg.SentryEnabled() {
		worker.OnDeadLetter = func(kind string, err error) {
			reporter.Capture(fmt.Errorf("job %s dead-lettered: %w", kind, err))
		}
	}
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
