// Command keelage-server is the team server: ledger, sync, GitHub App,
// webhooks and (after week 12) the embedded web UI.
// Week 1: an empty HTTP server with /healthz.
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

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/adapter/httpapi"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "keelage-server",
		Short:        "keelage team server",
		SilenceUsage: true,
	}
	root.AddCommand(serveCmd(), &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run:   func(cmd *cobra.Command, _ []string) { _, _ = fmt.Fprintln(cmd.OutOrStdout(), version) },
	})
	return root
}

func serveCmd() *cobra.Command {
	var addr string
	c := &cobra.Command{
		Use:   "serve",
		Short: "Serve the HTTP API",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			srv := &http.Server{
				Addr:              addr,
				Handler:           httpapi.New("keelage-server", version),
				ReadHeaderTimeout: 5 * time.Second,
			}
			errc := make(chan error, 1)
			go func() { errc <- srv.ListenAndServe() }()
			log.Info("server starting", "version", version, "addr", addr)
			select {
			case err := <-errc:
				return err
			case <-ctx.Done():
			}
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			log.Info("server stopped")
			return nil
		},
	}
	c.Flags().StringVar(&addr, "addr", "127.0.0.1:8080", "listen address")
	return c
}
