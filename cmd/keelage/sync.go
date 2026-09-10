package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/adapter/keys"
	"github.com/young1ll/keelage/internal/adapter/teamclient"
	"github.com/young1ll/keelage/internal/app"
)

// serverConfig is ~/.keelage/server.json (0600): where this daemon syncs to.
type serverConfig struct {
	URL      string    `json:"url"`
	Org      string    `json:"org"`
	User     string    `json:"user"`
	Token    string    `json:"token"`
	LoggedIn time.Time `json:"logged_in"`
}

func serverConfigPath(home string) string { return filepath.Join(home, "server.json") }
func syncStatePath(home string) string    { return filepath.Join(home, "sync.json") }
func teamCachePath(home string) string    { return filepath.Join(home, "cache", "team.db") }

func loadServerConfig(home string) (*serverConfig, error) {
	b, err := os.ReadFile(serverConfigPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil, teamclient.ErrNoServer
	}
	if err != nil {
		return nil, err
	}
	var c serverConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.URL == "" || c.Token == "" {
		return nil, teamclient.ErrNoServer
	}
	return &c, nil
}

func saveJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

func loadSyncState(home, daemonID string) (*app.SyncState, error) {
	st := &app.SyncState{DaemonID: daemonID}
	b, err := os.ReadFile(syncStatePath(home))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, err
	}
	if st.DaemonID != daemonID {
		// the key changed: the server knows a different daemon; start over
		return &app.SyncState{DaemonID: daemonID}, nil
	}
	return st, nil
}

// teamClient opens the configured server with the saved token.
func teamClient(home string) (*teamclient.Client, *serverConfig, error) {
	cfg, err := loadServerConfig(home)
	if err != nil {
		return nil, nil, err
	}
	return teamclient.New(cfg.URL, cfg.Token), cfg, nil
}

// daemonID is the fingerprint of the daemon key: stable, and what the
// server binds the public key to.
func (c *coreRuntime) daemonID() string { return c.key.Fingerprint() }

func (c *coreRuntime) syncer(client *teamclient.Client) *app.Syncer {
	return &app.Syncer{
		Local: c.ledger, Cache: c.cache, Codec: c.codec, Client: client, DaemonID: c.daemonID(),
		Constraints: c.constraints, Sessions: c.sessions, Projectors: c.projectors(), Clock: clock{},
	}
}

func serverCmd() *cobra.Command {
	c := &cobra.Command{Use: "server", Short: "Log in to a team server and inspect its ledger"}
	c.AddCommand(serverLoginCmd(), serverLogoutCmd(), serverKeyCmd(), serverProofCmd())
	return c
}

func serverLoginCmd() *cobra.Command {
	var url, org, token string
	c := &cobra.Command{
		Use:   "login",
		Short: "Log in (GitHub device flow, or --token) and register this daemon's key",
		Long: `Without --token the server starts a GitHub device login: enter the code shown at
the URL shown, and a keelage token for --org is issued to members of the org.
The daemon's public key is then registered so its pushed records verify.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if url == "" {
				return errors.New("--url is required")
			}
			rt, err := openCore(ctx)
			if err != nil {
				return err
			}
			defer rt.close()
			cfg := &serverConfig{URL: url, Org: org, Token: token}
			if token == "" {
				if org == "" {
					return errors.New("--org is required for device login")
				}
				if cfg.Token, cfg.User, err = deviceLogin(ctx, cmd, url, org); err != nil {
					return err
				}
			}
			client := teamclient.New(cfg.URL, cfg.Token)
			me, err := client.Me(ctx)
			if err != nil {
				return fmt.Errorf("login: %w", err)
			}
			cfg.Org, cfg.User, cfg.LoggedIn = me.Org, me.User, time.Now()
			if err := client.RegisterDaemon(ctx, rt.daemonID(), keys.EncodePublic(rt.key.Public)); err != nil {
				return fmt.Errorf("register daemon: %w", err)
			}
			if err := saveJSON(serverConfigPath(rt.home), cfg); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "logged in to %s as %s (org %s, %s); daemon %s registered\n", cfg.URL, cfg.User, cfg.Org, me.Kind, rt.daemonID())
			return nil
		},
	}
	c.Flags().StringVar(&url, "url", "", "server URL, e.g. https://keelage.example.com")
	c.Flags().StringVar(&org, "org", "", "org to log in to (device login)")
	c.Flags().StringVar(&token, "token", os.Getenv("KEELAGE_TOKEN"), "use an issued token instead of device login (or $KEELAGE_TOKEN)")
	return c
}

// deviceLogin runs the device flow against the server, which proxies GitHub.
func deviceLogin(ctx context.Context, cmd *cobra.Command, url, org string) (string, string, error) {
	client := teamclient.New(url, "")
	start, err := client.DeviceStart(ctx)
	if err != nil {
		return "", "", fmt.Errorf("device login: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Open %s and enter the code: %s\n", start.VerificationURI, start.UserCode)
	interval := time.Duration(start.Interval) * time.Second
	if interval < time.Second {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(start.ExpiresIn) * time.Second)
	if start.ExpiresIn <= 0 {
		deadline = time.Now().Add(15 * time.Minute)
	}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(interval):
		}
		res, err := client.DevicePoll(ctx, org, start.DeviceCode)
		if err != nil {
			return "", "", fmt.Errorf("device login: %w", err)
		}
		switch res.Status {
		case "ok":
			return res.Token, res.User, nil
		case "slow_down":
			if res.Interval > 0 {
				interval = time.Duration(res.Interval) * time.Second
			} else {
				interval += 5 * time.Second
			}
		case "denied":
			return "", "", errors.New("device login: denied")
		case "expired":
			return "", "", errors.New("device login: code expired; run login again")
		}
	}
	return "", "", errors.New("device login: timed out")
}

func serverLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Forget the server and token (the team cache stays)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome()
			if err != nil {
				return err
			}
			if err := os.Remove(serverConfigPath(home)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "logged out")
			return nil
		},
	}
}

func serverKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "key",
		Short: "Print the server's checkpoint public key",
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome()
			if err != nil {
				return err
			}
			client, _, err := teamClient(home)
			if err != nil {
				return err
			}
			k, err := client.PublicKey(cmd.Context())
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), k)
			return nil
		},
	}
}

func serverProofCmd() *cobra.Command {
	var seq int64
	var from, to uint64
	c := &cobra.Command{
		Use:   "proof",
		Short: "Fetch an inclusion (--seq) or consistency (--from --to) bundle as JSON, for `keelage verify-proof`",
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome()
			if err != nil {
				return err
			}
			client, _, err := teamClient(home)
			if err != nil {
				return err
			}
			var v any
			switch {
			case seq > 0:
				v, err = client.Inclusion(cmd.Context(), seq)
			case to > 0:
				v, err = client.Consistency(cmd.Context(), from, to)
			default:
				v, err = client.Checkpoint(cmd.Context())
			}
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(v)
		},
	}
	c.Flags().Int64Var(&seq, "seq", 0, "server seq to prove inclusion of")
	c.Flags().Uint64Var(&from, "from", 0, "consistency: earlier tree size")
	c.Flags().Uint64Var(&to, "to", 0, "consistency: later tree size")
	return c
}

func syncCmd() *cobra.Command {
	c := &cobra.Command{Use: "sync", Short: "Exchange records with the team server (push at commit/share, pull the team layer)"}
	var asJSON bool
	push := &cobra.Command{
		Use:   "push",
		Short: "Offer shareable local records (constraints, decisions, scopes, anchors, changes, gates; sessions only when shared)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			rt, err := openCore(ctx)
			if err != nil {
				return err
			}
			defer rt.close()
			client, _, err := teamClient(rt.home)
			if err != nil {
				return err
			}
			st, err := loadSyncState(rt.home, rt.daemonID())
			if err != nil {
				return err
			}
			rep, perr := rt.syncer(client).Push(ctx, st)
			if err := saveJSON(syncStatePath(rt.home), st); err != nil {
				return err
			}
			if perr != nil {
				return perr
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(rep)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "pushed: scanned %d, offered %d, accepted %d, rejected %d (server head %d)\n", rep.Scanned, rep.Offered, rep.Accepted, len(rep.Rejected), rep.Head)
			for _, r := range rep.Rejected {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  rejected local seq %d: %s: %s\n", r.LocalSeq, r.Code, r.Reason)
			}
			return nil
		},
	}
	pull := &cobra.Command{
		Use:   "pull",
		Short: "Copy the team layer (constraints, decisions, scopes, anchor history) into the local cache",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			rt, err := openCore(ctx)
			if err != nil {
				return err
			}
			defer rt.close()
			client, _, err := teamClient(rt.home)
			if err != nil {
				return err
			}
			st, err := loadSyncState(rt.home, rt.daemonID())
			if err != nil {
				return err
			}
			rep, perr := rt.syncer(client).Pull(ctx, st)
			if err := saveJSON(syncStatePath(rt.home), st); err != nil {
				return err
			}
			if perr != nil {
				return perr
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(rep)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "pulled: received %d, cached %d, skipped %d own (cursor %d)\n", rep.Received, rep.Cached, rep.Skipped, rep.Cursor)
			return nil
		},
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show the server, the daemon id and the sync cursors",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			rt, err := openCore(ctx)
			if err != nil {
				return err
			}
			defer rt.close()
			cfg, err := loadServerConfig(rt.home)
			if err != nil {
				return err
			}
			st, err := loadSyncState(rt.home, rt.daemonID())
			if err != nil {
				return err
			}
			head, _ := rt.ledger.Head(ctx)
			cacheHead, _ := rt.cache.Head(ctx)
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"server": cfg.URL, "org": cfg.Org, "user": cfg.User, "daemon_id": rt.daemonID(),
					"local_head": head.Seq, "push_cursor": st.PushCursor, "pull_cursor": st.PullCursor, "cache_records": cacheHead.Seq,
				})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "server %s · org %s · user %s\ndaemon %s\nlocal head %d · pushed through %d · pulled through server seq %d · %d team records cached\n",
				cfg.URL, cfg.Org, cfg.User, rt.daemonID(), head.Seq, st.PushCursor, st.PullCursor, cacheHead.Seq)
			return nil
		},
	}
	c.PersistentFlags().BoolVar(&asJSON, "json", false, "print as JSON")
	c.AddCommand(push, pull, status)
	return c
}
