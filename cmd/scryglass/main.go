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

	"github.com/runlevel-six/scryglass/internal/bookmarks"
	"github.com/runlevel-six/scryglass/internal/catalog"
	"github.com/runlevel-six/scryglass/internal/config"
	"github.com/runlevel-six/scryglass/internal/kube"
	"github.com/runlevel-six/scryglass/internal/probe"
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

	prober := probe.New(probe.Options{
		Interval: cfg.ProbeInterval,
		Timeout:  cfg.ProbeTimeout,
		Logger:   log,
	})

	// Bookmarks are optional: with no database path configured the binary is
	// exactly what it was before M3, which keeps it runnable outside a cluster
	// and with no volume attached.
	var marks *bookmarks.Store

	if cfg.DatabasePath != "" {
		if marks, err = bookmarks.Open(cfg.DatabasePath); err != nil {
			return err
		}

		defer marks.Close()
	}

	srv, err := server.New(cfg, cat, prober, marks, log)
	if err != nil {
		return err
	}

	// Without a cluster to discover from, the file is the whole catalog. That
	// is M1's behavior and it stays reachable: running this outside
	// Kubernetes should degrade to a working omnibox, not fail to start.
	var discoverer *kube.Client

	if cfg.Discovery {
		discoverer, err = kube.InCluster()
		if err != nil {
			return fmt.Errorf("discovery is enabled but the cluster is unreachable: %w", err)
		}
	}

	apply := func(discovered []catalog.Resource) {
		merged := catalog.Merge(cat.Resources, discovered)
		prober.SetResources(merged)
		srv.SetResources(merged)
	}

	apply(nil)

	log.Info("starting",
		"version", version.Version,
		"addr", cfg.Addr,
		"base_url", cfg.BaseURL,
		"resources", len(cat.Resources),
		"bangs", len(cat.Bangs),
		"discovery", cfg.Discovery,
		"bookmarks", cfg.DatabasePath != "",
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

	go prober.Run(ctx)

	if discoverer != nil {
		go runDiscovery(ctx, discoverer, cfg, log, apply)
	}

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

// runDiscovery refreshes the resource list from the cluster on an interval.
//
// A failed pass logs and keeps the previous list rather than emptying the
// wall. An API blip should not blank the dashboard, and a dashboard that
// blanks itself is one nobody trusts afterwards.
func runDiscovery(
	ctx context.Context,
	client *kube.Client,
	cfg *config.Config,
	log *slog.Logger,
	apply func([]catalog.Resource),
) {
	opt := kube.Options{
		AnnotationPrefix: cfg.AnnotationPrefix,
		SelfHost:         cfg.SelfHost(),
	}

	refresh := func() {
		discovered, err := client.Discover(ctx, opt)
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("discovery failed; keeping the previous resources", "error", err)
			}

			return
		}

		log.Debug("discovered", "count", len(discovered))
		apply(discovered)
	}

	refresh()

	ticker := time.NewTicker(cfg.DiscoveryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}
