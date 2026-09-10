// Command keelage-server is the team server (implementation-plan-v0 §4):
// the Postgres ledger with Merkle checkpoints and proofs, the sync
// receiver for daemons, the approval broker (/gates) and device login.
// Weeks 10–11: serve, org/member/token administration, checkpoint.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/adapter/github"
	"github.com/young1ll/keelage/internal/adapter/githubapp"
	"github.com/young1ll/keelage/internal/adapter/httpapi"
	"github.com/young1ll/keelage/internal/adapter/keys"
	"github.com/young1ll/keelage/internal/adapter/postgres"
	"github.com/young1ll/keelage/internal/app"
	"github.com/young1ll/keelage/internal/merkle"
	"github.com/young1ll/keelage/internal/port"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// dbFlag is the DSN, from --db or $KEELAGE_DB.
func dbFlag(c *cobra.Command, db *string) {
	c.Flags().StringVar(db, "db", os.Getenv("KEELAGE_DB"), "postgres DSN (or $KEELAGE_DB)")
}

func openStore(ctx context.Context, dsn string, signer port.Signer) (*postgres.Store, error) {
	if dsn == "" {
		return nil, errors.New("--db (or $KEELAGE_DB) is required")
	}
	return postgres.Open(ctx, dsn, signer)
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "keelage-server",
		Short:        "keelage team server",
		SilenceUsage: true,
	}
	root.AddCommand(serveCmd(), orgCmd(), memberCmd(), tokenCmd(), checkpointCmd(), keyCmd(), &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run:   func(cmd *cobra.Command, _ []string) { _, _ = fmt.Fprintln(cmd.OutOrStdout(), version) },
	})
	return root
}

type clock struct{}

func (clock) Now() time.Time { return time.Now() }

// proofs serves checkpoints and proofs from the store, signing a fresh
// checkpoint when the head moved past the last one (so any record can be
// proven right after it is written).
type proofs struct {
	store *postgres.Store
	key   *keys.Key
}

func (p *proofs) Checkpoint(ctx context.Context, org string) (merkle.Checkpoint, error) {
	return p.store.Ledger(org).Checkpoint(ctx, p.key.Private, p.key.Fingerprint(), time.Now())
}

func (p *proofs) Inclusion(ctx context.Context, org string, seq int64) (merkle.Bundle, error) {
	l := p.store.Ledger(org)
	head, err := l.Head(ctx)
	if err != nil {
		return merkle.Bundle{}, err
	}
	if seq > head.Seq {
		return merkle.Bundle{}, fmt.Errorf("%w: seq %d is past the head (%d)", port.ErrNotFound, seq, head.Seq)
	}
	if _, err := l.Checkpoint(ctx, p.key.Private, p.key.Fingerprint(), time.Now()); err != nil {
		return merkle.Bundle{}, err
	}
	return l.InclusionBundle(ctx, seq)
}

func (p *proofs) Consistency(ctx context.Context, org string, from, to uint64) (merkle.ConsistencyBundle, error) {
	return p.store.Ledger(org).ConsistencyBundle(ctx, from, to)
}

func serveCmd() *cobra.Command {
	var addr, db, keyPath, ghClientID, ghOAuth, ghAPI, appID, appKey, webhookSecret string
	var cpInterval, tokenTTL time.Duration
	c := &cobra.Command{
		Use:   "serve",
		Short: "Serve the team API",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			key, err := keys.LoadOrCreate(keyPath)
			if err != nil {
				return err
			}
			store, err := openStore(ctx, db, key)
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			team := &app.TeamServer{
				Orgs: &app.Orgs{
					Open:  func(_ context.Context, org string) (port.OriginLedger, error) { return store.Ledger(org), nil },
					Codec: app.NewCodec(), Clock: clock{},
				},
				Daemons: store, Keys: keys.Verifier{}, Clock: clock{},
			}
			srvAPI := &httpapi.Server{Auth: store, Team: team, Proofs: &proofs{store: store, key: key}, PublicKey: keys.EncodePublic(key.Public)}
			if ghClientID != "" {
				srvAPI.Login = &app.Login{
					OAuth: &github.Device{ClientID: ghClientID, OAuthBase: ghOAuth, APIBase: ghAPI}, Members: store, Tokens: store, TTL: tokenTTL,
				}
			}
			r := httpapi.New("keelage-server", version)
			httpapi.MountServer(r, srvAPI)
			// GitHub App (spec §4.5): PR comments and checks from pushed Changes, reviews → Judged
			if appID != "" || webhookSecret != "" {
				if appID == "" || appKey == "" || webhookSecret == "" {
					return errors.New("--github-app-id, --github-app-key and --github-webhook-secret go together")
				}
				pemBytes, err := os.ReadFile(appKey)
				if err != nil {
					return err
				}
				priv, err := githubapp.ParsePrivateKey(pemBytes)
				if err != nil {
					return err
				}
				bridge := &app.PRBridge{Orgs: team.Orgs, Reporter: &githubapp.App{AppID: appID, PrivateKey: priv, APIBase: ghAPI}, Installs: store, Members: store, Clock: clock{}}
				httpapi.MountWebhooks(r, webhookSecret, bridge.Sink(githubapp.Parse))
			}
			srv := &http.Server{Addr: addr, Handler: r, ReadHeaderTimeout: 5 * time.Second}
			errc := make(chan error, 1)
			go func() { errc <- srv.ListenAndServe() }()
			go checkpointWorker(ctx, log, store, key, cpInterval)
			log.Info("server starting", "version", version, "addr", addr, "public_key", keys.EncodePublic(key.Public), "device_login", ghClientID != "", "github_app", appID != "")
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
	dbFlag(c, &db)
	c.Flags().StringVar(&keyPath, "key", "server.ed25519", "checkpoint signing key file (created if missing, 0600)")
	c.Flags().StringVar(&ghClientID, "github-client-id", os.Getenv("KEELAGE_GITHUB_CLIENT_ID"), "GitHub OAuth app client id for device login (or $KEELAGE_GITHUB_CLIENT_ID)")
	c.Flags().StringVar(&ghOAuth, "github-oauth-base", "", "GitHub OAuth base URL (default https://github.com)")
	c.Flags().StringVar(&ghAPI, "github-api-base", "", "GitHub API base URL (default https://api.github.com)")
	c.Flags().StringVar(&appID, "github-app-id", os.Getenv("KEELAGE_GITHUB_APP_ID"), "GitHub App id (PR comments·checks; or $KEELAGE_GITHUB_APP_ID)")
	c.Flags().StringVar(&appKey, "github-app-key", os.Getenv("KEELAGE_GITHUB_APP_KEY"), "GitHub App private key PEM file (or $KEELAGE_GITHUB_APP_KEY)")
	c.Flags().StringVar(&webhookSecret, "github-webhook-secret", os.Getenv("KEELAGE_GITHUB_WEBHOOK_SECRET"), "webhook secret for /webhooks/github (or $KEELAGE_GITHUB_WEBHOOK_SECRET)")
	c.Flags().DurationVar(&cpInterval, "checkpoint-interval", 10*time.Minute, "how often to sign a checkpoint for orgs whose ledger moved")
	c.Flags().DurationVar(&tokenTTL, "token-ttl", 90*24*time.Hour, "lifetime of tokens issued by device login (0: no expiry)")
	return c
}

// checkpointWorker signs a checkpoint for every org whose head moved
// (spec §4.3: periodic; the proof endpoints also sign on demand).
func checkpointWorker(ctx context.Context, log *slog.Logger, store *postgres.Store, key *keys.Key, every time.Duration) {
	if every <= 0 {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		orgs, err := store.Orgs(ctx)
		if err != nil {
			log.Warn("checkpoint: list orgs", "err", err)
			continue
		}
		for _, org := range orgs {
			l := store.Ledger(org)
			head, err := l.Head(ctx)
			if err != nil || head.Seq == 0 {
				continue
			}
			if cp, err := l.LatestCheckpoint(ctx); err == nil && cp.TreeSize == uint64(head.Seq) {
				continue
			}
			if cp, err := l.Checkpoint(ctx, key.Private, key.Fingerprint(), time.Now()); err != nil {
				log.Warn("checkpoint", "org", org, "err", err)
			} else {
				log.Info("checkpoint", "org", org, "tree_size", cp.TreeSize)
			}
		}
	}
}

func orgCmd() *cobra.Command {
	c := &cobra.Command{Use: "org", Short: "Manage tenants"}
	var db string
	create := &cobra.Command{
		Use:   "create <org>",
		Short: "Create an org (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore(cmd.Context(), db, nil)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			if err := s.EnsureOrg(cmd.Context(), args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "org %s ready\n", args[0])
			return nil
		},
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List orgs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := openStore(cmd.Context(), db, nil)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			orgs, err := s.Orgs(cmd.Context())
			if err != nil {
				return err
			}
			for _, o := range orgs {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), o)
			}
			return nil
		},
	}
	link := &cobra.Command{
		Use:   "link-installation <org> <installation-id>",
		Short: "Bind a GitHub App installation to an org (the `installation` webhook does this automatically)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("installation id: %w", err)
			}
			s, err := openStore(cmd.Context(), db, nil)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			if err := s.LinkInstallation(cmd.Context(), id, args[0]); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "installation %d → org %s\n", id, args[0])
			return nil
		},
	}
	c.PersistentFlags().StringVar(&db, "db", os.Getenv("KEELAGE_DB"), "postgres DSN (or $KEELAGE_DB)")
	c.AddCommand(create, list, link)
	return c
}

func memberCmd() *cobra.Command {
	c := &cobra.Command{Use: "member", Short: "Manage org members"}
	var db, role string
	add := &cobra.Command{
		Use:   "add <org> <user>",
		Short: "Add a member (user = GitHub login for device login)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore(cmd.Context(), db, nil)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			if err := s.EnsureOrg(cmd.Context(), args[0]); err != nil {
				return err
			}
			if err := s.AddMember(cmd.Context(), args[0], args[1], role); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s is a %s of %s\n", args[1], role, args[0])
			return nil
		},
	}
	add.Flags().StringVar(&role, "role", "member", "role: member | owner")
	c.PersistentFlags().StringVar(&db, "db", os.Getenv("KEELAGE_DB"), "postgres DSN (or $KEELAGE_DB)")
	c.AddCommand(add)
	return c
}

func tokenCmd() *cobra.Command {
	c := &cobra.Command{Use: "token", Short: "Issue and revoke bearer tokens"}
	var db, kind, owner, label string
	var ttl time.Duration
	create := &cobra.Command{
		Use:   "create <org> <user>",
		Short: "Issue a token; agent and ci tokens name the responsible human (--owner)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore(cmd.Context(), db, nil)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			if kind == "human" {
				owner = ""
			}
			tok, err := s.IssueToken(cmd.Context(), args[0], args[1], kind, owner, label, ttl)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), tok)
			return nil
		},
	}
	create.Flags().StringVar(&kind, "kind", "human", "human | agent | ci")
	create.Flags().StringVar(&owner, "owner", "", "responsible human for agent/ci tokens")
	create.Flags().StringVar(&label, "label", "", "label")
	create.Flags().DurationVar(&ttl, "ttl", 0, "lifetime (0: no expiry)")
	revoke := &cobra.Command{
		Use:   "revoke <token>",
		Short: "Revoke a token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openStore(cmd.Context(), db, nil)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			return s.RevokeToken(cmd.Context(), args[0])
		},
	}
	c.PersistentFlags().StringVar(&db, "db", os.Getenv("KEELAGE_DB"), "postgres DSN (or $KEELAGE_DB)")
	c.AddCommand(create, revoke)
	return c
}

func checkpointCmd() *cobra.Command {
	var db, keyPath string
	c := &cobra.Command{
		Use:   "checkpoint <org>",
		Short: "Sign a checkpoint of the org's ledger head now",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := keys.LoadOrCreate(keyPath)
			if err != nil {
				return err
			}
			s, err := openStore(cmd.Context(), db, key)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			cp, err := s.Ledger(args[0]).Checkpoint(cmd.Context(), key.Private, key.Fingerprint(), time.Now())
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: tree_size=%d root=%x at=%s key=%s\n", cp.Org, cp.TreeSize, cp.Root, cp.At.Format(time.RFC3339), cp.KeyID)
			return nil
		},
	}
	dbFlag(c, &db)
	c.Flags().StringVar(&keyPath, "key", "server.ed25519", "checkpoint signing key file")
	return c
}

func keyCmd() *cobra.Command {
	var keyPath string
	c := &cobra.Command{
		Use:   "key",
		Short: "Print the server's public key (what auditors pass to `keelage verify-proof --server-key`)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := keys.LoadOrCreate(keyPath)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), keys.EncodePublic(key.Public))
			return nil
		},
	}
	c.Flags().StringVar(&keyPath, "key", "server.ed25519", "checkpoint signing key file (created if missing)")
	return c
}
