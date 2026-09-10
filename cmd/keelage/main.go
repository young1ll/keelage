// Command keelage is the single binary: daemon, CLI, hook endpoint, local MCP.
// Week 1: an empty daemon that boots, serves /healthz over a Unix socket,
// and exits cleanly on SIGINT/SIGTERM.
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/adapter/httpapi"
	"github.com/young1ll/keelage/internal/adapter/uds"
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
		Use:           "keelage",
		Short:         "keelage — the intent·constraint·accountability layer between people and autonomous systems",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.AddCommand(daemonCmd(), versionCmd())
	return root
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), version)
		},
	}
}

func daemonCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "daemon",
		Short: "Run the per-user daemon (local API over a Unix socket)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			sock, err := resolveSocket(socket)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			log.Info("daemon starting", "version", version, "socket", sock)
			err = uds.Serve(ctx, sock, httpapi.New("keelage", version))
			if errors.Is(err, uds.ErrAlreadyRunning) {
				return fmt.Errorf("%w: %s", err, sock)
			}
			log.Info("daemon stopped")
			return err
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "Unix socket path (default ~/.keelage/keelage.sock)")
	return c
}

func resolveSocket(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if env := os.Getenv("KEELAGE_HOME"); env != "" {
		return filepath.Join(env, "keelage.sock"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".keelage", "keelage.sock"), nil
}
