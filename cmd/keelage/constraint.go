package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/app"
	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/harness"
	"github.com/young1ll/keelage/internal/core/supply"
)

func constraintCmd() *cobra.Command {
	var repoDir string
	c := &cobra.Command{Use: "constraint", Short: "Author and inspect constraints (the charter)"}
	c.PersistentFlags().StringVar(&repoDir, "repo", ".", "repository directory")

	var kind, scope, level, explanation string
	var anchors []string
	var denyEdit, verify bool
	add := &cobra.Command{
		Use:   "add <body...>",
		Short: "Draft an authored constraint (and optionally verify it)",
		Long: `Scope: "repo" (this repository), "path=<glob>" (paths in this repository),
"team=<name>", "product=<name>", "global" or "personal".
An autonomy constraint with --deny-edit blocks agent edits in its scope.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			h, err := openHeadless(ctx, repoDir)
			if err != nil {
				return err
			}
			defer h.close(ctx)
			key, err := parseScope(scope, app.RepoID(h.repo.Root), string(h.actor.ID))
			if err != nil {
				return err
			}
			id := h.ids.New()
			draft := harness.DraftConstraint{
				ID: id, ConstraintKind: harness.ConstraintKind(kind), Scope: key, Body: strings.Join(args, " "),
				Authored: true, Explanation: explanation, Anchors: anchors,
			}
			if kind == string(harness.KindAutonomy) {
				if draft.Level, err = core.ParseLevel(level); err != nil {
					return err
				}
				if denyEdit {
					draft.Checkable = supply.DenyEditMarker
				}
			} else if denyEdit {
				return fmt.Errorf("--deny-edit applies to --kind autonomy only")
			}
			if _, err := h.pipeline.Handle(ctx, h.actor, draft); err != nil {
				return err
			}
			if verify {
				if _, err := h.pipeline.Handle(ctx, h.actor, harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: id}}); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), id)
			return nil
		},
	}
	add.Flags().StringVar(&kind, "kind", "rule", "contract|invariant|rule|budget|autonomy")
	add.Flags().StringVar(&scope, "scope", "repo", "repo | path=<glob> | team=<name> | product=<name> | global | personal")
	add.Flags().StringVar(&level, "level", "L0", "autonomy level (kind autonomy)")
	add.Flags().StringArrayVar(&anchors, "anchor", nil, "anchor the constraint binds (repeatable)")
	add.Flags().StringVar(&explanation, "explanation", "", "one human-readable paragraph")
	add.Flags().BoolVar(&denyEdit, "deny-edit", false, "autonomy constraint denies agent edits in its scope")
	add.Flags().BoolVar(&verify, "verify", false, "verify right away (a human act)")

	verifyCmd := &cobra.Command{
		Use:   "verify <id>...",
		Short: "Mark constraints verified",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			h, err := openHeadless(ctx, repoDir)
			if err != nil {
				return err
			}
			defer h.close(ctx)
			for _, id := range args {
				if _, err := h.pipeline.Handle(ctx, h.actor, harness.VerifyConstraint{ConstraintCmd: harness.ConstraintCmd{ID: core.ID(id)}}); err != nil {
					return err
				}
			}
			return nil
		},
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List constraints",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			h, err := openHeadless(ctx, repoDir)
			if err != nil {
				return err
			}
			defer h.close(ctx)
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "ID\tKIND\tSTATE\tSCOPE\tANCHORS\tBODY")
			for _, c := range h.constraints.All() {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", c.ID, c.Kind, c.State, c.Scope, strings.Join(c.Anchors, ","), c.Body)
			}
			return w.Flush()
		},
	}
	c.AddCommand(add, verifyCmd, list)
	return c
}

func parseScope(s, repo, person string) (core.ScopeKey, error) {
	switch {
	case s == "repo":
		return core.ScopeKey{Repo: repo}, nil
	case s == "global":
		return core.ScopeKey{}, nil
	case s == "personal":
		return core.ScopeKey{Person: person}, nil
	case strings.HasPrefix(s, "path="):
		return core.ScopeKey{Repo: repo, PathGlob: strings.TrimPrefix(s, "path=")}, nil
	case strings.HasPrefix(s, "team="):
		return core.ScopeKey{Team: strings.TrimPrefix(s, "team=")}, nil
	case strings.HasPrefix(s, "product="):
		return core.ScopeKey{Product: strings.TrimPrefix(s, "product=")}, nil
	}
	return core.ScopeKey{}, fmt.Errorf("unknown scope %q", s)
}
