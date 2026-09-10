package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/adapter/git"
	"github.com/young1ll/keelage/internal/adapter/keys"
	"github.com/young1ll/keelage/internal/adapter/sqlite"
	"github.com/young1ll/keelage/internal/adapter/treesitter"
	"github.com/young1ll/keelage/internal/adapter/ulid"
	"github.com/young1ll/keelage/internal/app"
	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/realization"
	"github.com/young1ll/keelage/internal/port"
)

// coreRuntime is the in-process assembly shared by the daemon and the
// headless commands: the personal ledger, the projections rebuilt from it,
// the pipeline and the hook service.
type coreRuntime struct {
	home        string
	key         *keys.Key
	ledger      *sqlite.Ledger
	cache       *sqlite.Ledger // team layer pulled from the server (~/.keelage/cache/team.db)
	codec       *core.Codec
	scopes      *app.ScopeIndex
	constraints *app.ConstraintIndex
	changes     *app.ChangeIndex
	anchors     *app.AnchorIndex
	sessions    *app.SessionIndex
	decisions   *app.DecisionIndex
	pipeline    *app.Pipeline
	hooks       *app.Hooks
	ids         port.IDGen
	// cursors track what the projections have seen per ledger, including
	// the pipeline's own synchronous writes, so catch-up never re-applies.
	cursor      *app.Cursor
	cacheCursor *app.Cursor
	mu          sync.Mutex
}

// clock is the wall clock.
type clock struct{}

func (clock) Now() time.Time { return time.Now() }

func openCore(ctx context.Context) (*coreRuntime, error) {
	home, err := resolveHome()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(home, "cache"), 0o700); err != nil {
		return nil, fmt.Errorf("create home: %w", err)
	}
	// every local record is signed with the daemon key (~/.keelage/keys, 0600)
	key, err := keys.LoadOrCreate(filepath.Join(home, "keys", "daemon.ed25519"))
	if err != nil {
		return nil, err
	}
	ledger, err := sqlite.Open(filepath.Join(home, "ledger.db"), key)
	if err != nil {
		return nil, err
	}
	// the team cache keeps the server's copies as they are (no re-signing)
	cache, err := sqlite.Open(teamCachePath(home), nil)
	if err != nil {
		_ = ledger.Close()
		return nil, err
	}
	c := &coreRuntime{key: key,
		home: home, ledger: ledger, cache: cache, codec: app.NewCodec(),
		scopes: app.NewScopeIndex(), constraints: app.NewConstraintIndex(), changes: app.NewChangeIndex(), anchors: app.NewAnchorIndex(),
		sessions: app.NewSessionIndex(), decisions: app.NewDecisionIndex(), ids: ulid.New(),
		cursor: &app.Cursor{}, cacheCursor: &app.Cursor{},
	}
	if _, err = app.Rebuild(ctx, ledger, c.codec, c.projectors()...); err != nil {
		c.close()
		return nil, err
	}
	// team layer on top: streams this daemon never wrote locally (ADR 0015)
	if _, err = app.Rebuild(ctx, cache, c.codec, c.cacheProjectors()...); err != nil {
		c.close()
		return nil, err
	}
	c.pipeline = app.New(app.Options{
		Ledger: ledger, Codec: c.codec, Clock: clock{}, Context: app.ProjectionContext{Scopes: c.scopes, Constraints: c.constraints},
		Projectors: c.projectors(),
	})
	app.RegisterAll(c.pipeline)
	c.hooks = app.NewHooks(c.anchors, c.constraints, clock{}).WithCapture(c.pipeline, localUser())
	return c, nil
}

func (c *coreRuntime) indexes() []port.Projector {
	return []port.Projector{c.scopes, c.constraints, c.changes, c.anchors, c.sessions, c.decisions}
}

// projectors is the personal ledger's list (pipeline + catch-up).
func (c *coreRuntime) projectors() []port.Projector { return append(c.indexes(), c.cursor) }

// cacheProjectors is the team cache's list (pull + catch-up).
func (c *coreRuntime) cacheProjectors() []port.Projector { return append(c.indexes(), c.cacheCursor) }

// catchUp projects records written by other processes since the last look.
func (c *coreRuntime) catchUp(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	head, err := c.ledger.Head(ctx)
	if err != nil {
		return err
	}
	if head.Seq > c.cursor.Seq() {
		if _, err := app.Replay(ctx, c.ledger, c.codec, c.cursor.Seq(), c.projectors()...); err != nil {
			return err
		}
	}
	cacheHead, err := c.cache.Head(ctx)
	if err != nil {
		return err
	}
	if cacheHead.Seq > c.cacheCursor.Seq() {
		if _, err := app.Replay(ctx, c.cache, c.codec, c.cacheCursor.Seq(), c.cacheProjectors()...); err != nil {
			return err
		}
	}
	return nil
}

func (c *coreRuntime) close() {
	_ = c.ledger.Close()
	if c.cache != nil {
		_ = c.cache.Close()
	}
}

// headless adds a repository and the anchor resolver: used by commands that
// run without the daemon (CI, `verify`, `anchor`, `constraint`).
type headless struct {
	*coreRuntime
	repo     *git.Repo
	actor    core.ActorRef
	verifier *app.Verifier
	deriver  *app.Deriver
	ts       *treesitter.Runtime
}

// localUser identifies the person this daemon runs for (the owner of every
// agent actor it records): the server login once there is one (ADR 0015:
// the server checks that pushed records are the pushing user's own), else
// the OS user.
func localUser() core.ID {
	if home, err := resolveHome(); err == nil {
		if cfg, err := loadServerConfig(home); err == nil && cfg.User != "" {
			return core.ID(cfg.User)
		}
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return core.ID(u.Username)
	}
	return "local"
}

func openHeadless(ctx context.Context, repoDir string) (*headless, error) {
	c, err := openCore(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := git.Open(ctx, repoDir)
	if err != nil {
		c.close()
		return nil, err
	}
	h := &headless{coreRuntime: c, repo: repo}
	// identity: the server login, else the git author, else the OS user
	id := ""
	if cfg, err := loadServerConfig(c.home); err == nil {
		id = cfg.User
	}
	if id == "" {
		if id, err = repo.UserEmail(ctx); err != nil || id == "" {
			id = string(localUser())
		}
	}
	h.actor = core.ActorRef{Kind: core.ActorHuman, ID: core.ID(id)}
	h.ts, err = treesitter.NewRuntime(ctx, filepath.Join(c.home, "cache", "wazero"))
	if err != nil {
		c.close()
		return nil, err
	}
	parser := treesitter.NewResolver(h.ts)
	h.verifier = &app.Verifier{Pipeline: c.pipeline, Anchors: c.anchors, Resolver: app.NewResolver(repo.Root, parser)}
	h.deriver = &app.Deriver{
		Pipeline: c.pipeline, Commits: repo, Parser: parser, Constraints: c.constraints, Sessions: c.sessions, Changes: c.changes,
		IDs: c.ids, Repo: app.RepoID(repo.Root),
	}
	return h, nil
}

func (h *headless) close(ctx context.Context) {
	if h.ts != nil {
		_ = h.ts.Close(ctx)
	}
	h.coreRuntime.close()
}

func anchorCmd() *cobra.Command {
	c := &cobra.Command{Use: "anchor", Short: "Record and inspect realization anchors"}
	var repoDir, ref string
	add := &cobra.Command{
		Use:   "add <anchor>...",
		Short: "Record anchors at their current hash (e.g. code://src/a.ts#f)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			h, err := openHeadless(ctx, repoDir)
			if err != nil {
				return err
			}
			defer h.close(ctx)
			if ref == "" {
				ref, _ = h.repo.Head(ctx)
			}
			for _, a := range args {
				res, err := h.verifier.Record(ctx, h.actor, a, ref)
				if err != nil {
					return err
				}
				note := ""
				if res.Degraded {
					note = "  (degraded to file hash: no resolver for this language)"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  sig=%.12s body=%.12s file=%.12s%s\n", res.Anchor, res.Language, res.Hash.Signature, res.Hash.Body, res.Hash.File, note)
			}
			return nil
		},
	}
	add.Flags().StringVar(&ref, "ref", "", "commit the hash is taken at (default: HEAD)")
	list := &cobra.Command{
		Use:   "list",
		Short: "List recorded anchors and their state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			h, err := openHeadless(ctx, repoDir)
			if err != nil {
				return err
			}
			defer h.close(ctx)
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "ANCHOR\tSTATE\tREF\tSIGNATURE\tUPDATED")
			for _, a := range h.anchors.All() {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%.8s\t%.12s\t%s\n", a.Key, a.State, a.Ref, a.Hash.Signature, a.UpdatedAt.Local().Format("2006-01-02 15:04"))
			}
			return w.Flush()
		},
	}
	c.PersistentFlags().StringVar(&repoDir, "repo", ".", "repository directory")
	c.AddCommand(add, list)
	return c
}

func verifyCmd() *cobra.Command {
	var repoDir, ref, base, failOn string
	var changed, asJSON bool
	c := &cobra.Command{
		Use:   "verify",
		Short: "Check recorded anchors against the working tree (headless; used by CI)",
		Long: `verify resolves every recorded anchor and records the outcome in the ledger:
file-only change → verified · body change → review · signature change → stale ·
missing → unrealized. With --changed only anchors in files that differ from HEAD
(or --base) are checked, and moved symbols are detected across those files.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			h, err := openHeadless(ctx, repoDir)
			if err != nil {
				return err
			}
			defer h.close(ctx)
			if ref == "" {
				ref, _ = h.repo.Head(ctx)
			}
			var files []string
			if changed {
				if files, err = h.repo.ChangedFiles(ctx, base); err != nil {
					return err
				}
				if files == nil {
					files = []string{}
				}
			}
			report, err := h.verifier.Verify(ctx, h.actor, ref, files)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return err
				}
			} else {
				printReport(cmd, report)
			}
			return failure(report, failOn)
		},
	}
	c.Flags().StringVar(&repoDir, "repo", ".", "repository directory")
	c.Flags().StringVar(&ref, "ref", "", "commit to attribute this verify to (default: HEAD)")
	c.Flags().BoolVar(&changed, "changed", false, "only anchors in files changed since HEAD (or --base)")
	c.Flags().StringVar(&base, "base", "", "with --changed: compare against this commit instead of HEAD")
	c.Flags().StringVar(&failOn, "fail-on", "stale", "exit non-zero when any anchor is at least this bad: none|review|stale")
	c.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
	return c
}

func printReport(cmd *cobra.Command, r app.VerifyReport) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ANCHOR\tBEFORE\tAFTER\tCHANGE\tLANG")
	for _, x := range r.Results {
		change := x.Change.String()
		if !x.Exists && x.Skipped == "" {
			change = "missing"
		}
		if x.Skipped != "" {
			change = "skipped: " + x.Skipped
		}
		lang := x.Language
		if x.Degraded {
			lang += " (degraded)"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", x.Key, x.Before, x.After, change, lang)
	}
	_ = w.Flush()
	for _, m := range r.Moves {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "moved: %s → %s\n", m.From, m.To)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%d anchors · verified %d · review %d · stale %d · unrealized %d · ref %.8s\n",
		len(r.Results), r.Count(realization.StateVerified), r.Count(realization.StateReview), r.Count(realization.StateStale), r.Count(realization.StateUnrealized), r.Ref)
}

func failure(r app.VerifyReport, failOn string) error {
	bad := 0
	switch failOn {
	case "none":
		return nil
	case "review":
		bad = r.Count(realization.StateReview) + r.Count(realization.StateStale) + r.Count(realization.StateUnrealized)
	case "stale":
		bad = r.Count(realization.StateStale) + r.Count(realization.StateUnrealized)
	default:
		return fmt.Errorf("--fail-on: unknown level %q", failOn)
	}
	if bad > 0 {
		return errors.New("verify: stale anchors found")
	}
	return nil
}
