// Command keelage is the single binary: daemon, CLI, hook endpoint, local MCP.
// Week 1: an empty daemon that boots, serves /healthz over a Unix socket,
// and exits cleanly on SIGINT/SIGTERM.
package main

import (
	"context"
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
	"github.com/young1ll/keelage/internal/core/supply"
)

// synced serves hook and query requests after catching up on ledger writes
// made by other processes (the CLI writes to the same SQLite file).
type synced struct{ rt *coreRuntime }

func (s *synced) Hook(ctx context.Context, ev supply.Event) (supply.Response, error) {
	if err := s.rt.catchUp(ctx); err != nil {
		return supply.Response{}, err
	}
	return s.rt.hooks.Hook(ctx, ev)
}

func (s *synced) WhatTouches(ctx context.Context, anchor, repo string) (supply.Touches, error) {
	if err := s.rt.catchUp(ctx); err != nil {
		return supply.Touches{}, err
	}
	return s.rt.hooks.WhatTouches(ctx, anchor, repo)
}

func (s *synced) Related(ctx context.Context, id string) (supply.Related, error) {
	if err := s.rt.catchUp(ctx); err != nil {
		return supply.Related{}, err
	}
	return s.rt.hooks.Related(ctx, id)
}

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
	root.AddCommand(daemonCmd(), versionCmd(), anchorCmd(), verifyCmd(), constraintCmd(), hookCmd(), mcpCmd(), adapterCmd(), changeCmd(), inboxCmd(), historyCmd(), sessionsCmd(), initCmd(), uninitCmd(), statusCmd(), setupCmd(), uninstallCmd(), verifyProofCmd())
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
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			log := slog.New(slog.NewJSONHandler(os.Stderr, nil))

			rt, err := openCore(ctx)
			if err != nil {
				return err
			}
			defer rt.close()
			sock := socket
			if sock == "" {
				sock = filepath.Join(rt.home, "keelage.sock")
			}
			log.Info("daemon starting", "version", version, "socket", sock, "ledger_seq", rt.seq)

			r := httpapi.New("keelage", version)
			svc := &synced{rt: rt}
			httpapi.MountDaemon(r, svc, svc)
			err = uds.Serve(ctx, sock, r)
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

// resolveHome is ~/.keelage, or $KEELAGE_HOME.
func resolveHome() (string, error) {
	if env := os.Getenv("KEELAGE_HOME"); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".keelage"), nil
}
