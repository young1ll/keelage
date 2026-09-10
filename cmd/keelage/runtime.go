package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/adapter/git"
	"github.com/young1ll/keelage/internal/adapter/sqlite"
	"github.com/young1ll/keelage/internal/adapter/treesitter"
	"github.com/young1ll/keelage/internal/app"
	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/realization"
	"github.com/young1ll/keelage/internal/port"
)

// headless assembles the pipeline in-process: the personal ledger, the
// projections rebuilt from it, and the anchor resolver. Used by commands
// that run without the daemon (CI, `verify`, `anchor`).
type headless struct {
	home     string
	ledger   *sqlite.Ledger
	repo     *git.Repo
	actor    core.ActorRef
	anchors  *app.AnchorIndex
	pipeline *app.Pipeline
	verifier *app.Verifier
	ts       *treesitter.Runtime
}

// clock is the wall clock.
type clock struct{}

func (clock) Now() time.Time { return time.Now() }

func openHeadless(ctx context.Context, repoDir string) (*headless, error) {
	home, err := resolveHome()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(home, "cache"), 0o700); err != nil {
		return nil, fmt.Errorf("create home: %w", err)
	}
	repo, err := git.Open(ctx, repoDir)
	if err != nil {
		return nil, err
	}
	ledger, err := sqlite.Open(filepath.Join(home, "ledger.db"), nil)
	if err != nil {
		return nil, err
	}
	h := &headless{home: home, ledger: ledger, repo: repo}

	id, err := repo.UserEmail(ctx)
	if err != nil {
		if u, uerr := user.Current(); uerr == nil {
			id = u.Username
		} else {
			id = "local"
		}
	}
	h.actor = core.ActorRef{Kind: core.ActorHuman, ID: core.ID(id)}

	codec := app.NewCodec()
	scopes, constraints, changes := app.NewScopeIndex(), app.NewConstraintIndex(), app.NewChangeIndex()
	h.anchors = app.NewAnchorIndex()
	projectors := []port.Projector{scopes, constraints, changes, h.anchors}
	if _, err := app.Rebuild(ctx, ledger, codec, projectors...); err != nil {
		_ = ledger.Close()
		return nil, err
	}
	h.pipeline = app.New(app.Options{
		Ledger: ledger, Codec: codec, Clock: clock{}, Context: app.ProjectionContext{Scopes: scopes, Constraints: constraints},
		Projectors: projectors,
	})
	app.RegisterAll(h.pipeline)

	h.ts, err = treesitter.NewRuntime(ctx, filepath.Join(home, "cache", "wazero"))
	if err != nil {
		_ = ledger.Close()
		return nil, err
	}
	h.verifier = &app.Verifier{Pipeline: h.pipeline, Anchors: h.anchors, Resolver: app.NewResolver(repo.Root, treesitter.NewResolver(h.ts))}
	return h, nil
}

func (h *headless) close(ctx context.Context) {
	if h.ts != nil {
		_ = h.ts.Close(ctx)
	}
	_ = h.ledger.Close()
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
