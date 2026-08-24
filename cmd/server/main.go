// cmd/server is wiring only: host → generated Boot → serve → graceful shutdown.
// Every behavior lives in a module, and the dependency graph that assembles them
// is generated from the installed registry, so this file has nothing to say
// about which providers exist or what order they start in.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/modules"
)

// version is stamped at build time: -ldflags "-X main.version=$VERSION"
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	host := apphost.OS(version)
	log := host.Log()

	runtime, err := modules.Boot(ctx, host, modules.Options{})
	if err != nil {
		return err
	}
	// Shutdown gets a fresh context: the one that triggered it is already done.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runtime.Close(closeCtx); err != nil {
			log.Error("shutdown", "error", err)
		}
	}()

	cfg := runtime.Config
	log.Info("starting", "env", cfg.Env, "version", version, "port", cfg.Port)

	if err := runtime.Run(ctx); err != nil {
		return fmt.Errorf("start services: %w", err)
	}

	httpSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           runtime.Handler(),
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
