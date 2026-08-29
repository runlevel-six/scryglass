// Command scryglass serves the omnibox.
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

	"github.com/runlevel-six/scryglass/internal/catalog"
	"github.com/runlevel-six/scryglass/internal/config"
	"github.com/runlevel-six/scryglass/internal/server"
	"github.com/runlevel-six/scryglass/internal/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "scryglass: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// One subcommand, and it exists to be checked rather than read: CI runs
	// `docker run --rm <image> version` against a freshly published image and
	// fails the release if the binary does not report the tag it was published
	// under. That catches a stale layer or a build that silently lost its
	// -ldflags stamp, which otherwise ships looking fine.
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("scryglass %s\ncommit: %s\nbuilt:  %s\n",
			version.Version, version.Commit, version.Date)

		return nil
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	cat, err := catalog.Load(cfg.CatalogPath)
	if err != nil {
		return err
	}

	srv, err := server.New(cfg, cat, log)
	if err != nil {
		return err
	}

	log.Info("starting",
		"version", version.Version,
		"addr", cfg.Addr,
		"base_url", cfg.BaseURL,
		"resources", len(cat.Resources),
		"bangs", len(cat.Bangs),
	)

	httpSrv := &http.Server{
		Addr:    cfg.Addr,
		Handler: srv.Handler(),
		// A new-tab page has no long requests, so these are tight on purpose.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			return
		}

		errc <- nil
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return httpSrv.Shutdown(shutdownCtx)
}
