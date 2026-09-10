package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/app"
)

func inboxCmd() *cobra.Command {
	var repoDir string
	var asJSON bool
	c := &cobra.Command{
		Use:   "inbox",
		Short: "What needs a person: changes to judge, stale anchors, unverified constraints, session judgment candidates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			h, err := openHeadless(ctx, repoDir)
			if err != nil {
				return err
			}
			defer h.close(ctx)
			items := app.Inbox(h.changes, h.anchors, h.constraints, h.sessions)
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(items)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "KIND\tID\tSCOPE\tANCHOR\tSTATE\tCAUSE")
			for _, it := range items {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", it.Kind, short(it.ID), shortScope(it.Scope), it.Anchor, it.State, it.Cause)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%d item(s)\n", len(items))
			return nil
		},
	}
	c.Flags().StringVar(&repoDir, "repo", ".", "repository directory")
	c.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return c
}

func historyCmd() *cobra.Command {
	var repoDir string
	var asJSON bool
	c := &cobra.Command{
		Use:   "history <anchor>",
		Short: "Everything the ledger knows about an anchor, in order",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			h, err := openHeadless(ctx, repoDir)
			if err != nil {
				return err
			}
			defer h.close(ctx)
			entries, err := app.History(ctx, h.ledger, h.codec, args[0])
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(entries)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "SEQ\tAT\tKIND\tACTOR\tSUMMARY")
			for _, e := range entries {
				_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", e.Seq, e.At.Local().Format("2006-01-02 15:04"), e.Kind, e.Actor, e.Summary)
			}
			return w.Flush()
		},
	}
	c.Flags().StringVar(&repoDir, "repo", ".", "repository directory")
	c.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return c
}

func sessionsCmd() *cobra.Command {
	var repoDir string
	var asJSON bool
	c := &cobra.Command{
		Use:   "sessions",
		Short: "Recorded tool sessions (turns, touched files, judgment candidates — never the transcript)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			h, err := openHeadless(ctx, repoDir)
			if err != nil {
				return err
			}
			defer h.close(ctx)
			all := h.sessions.All()
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(all)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "SESSION\tTOOL\tREPO\tSTATE\tSTARTED\tTURNS\tFILES\tJUDGMENTS\tSUMMARY")
			for _, s := range all {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n", s.ID, s.Tool, s.Repo, s.State, s.StartedAt.Local().Format("2006-01-02 15:04"), s.Turns, len(s.Touched), len(s.Judgments), s.Summary)
			}
			return w.Flush()
		},
	}
	c.Flags().StringVar(&repoDir, "repo", ".", "repository directory")
	c.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return c
}

func changeCmd() *cobra.Command {
	var repoDir string
	c := &cobra.Command{Use: "change", Short: "Changes derived from commits"}
	c.PersistentFlags().StringVar(&repoDir, "repo", ".", "repository directory")
	var commit string
	var asJSON, noVerify bool
	derive := &cobra.Command{
		Use:   "derive",
		Short: "Derive the Change for a commit (run from the git post-commit hook)",
		Long: `The commit is the change boundary. Its diff resolves to anchors (symbols whose
signature or body changed; file anchors for other languages), the anchors to the
constraints in force. A change that touches no constraint settles at once; one
that does waits in the inbox for a judgment. Anchors in the changed files are
verified at the same commit unless --no-verify.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			h, err := openHeadless(ctx, repoDir)
			if err != nil {
				return err
			}
			defer h.close(ctx)
			d, err := h.deriver.Derive(ctx, h.actor, commit)
			if err != nil {
				return err
			}
			if !noVerify && len(d.Files) > 0 {
				if _, err := h.verifier.Verify(ctx, h.actor, d.SHA, d.Files); err != nil {
					return err
				}
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(d)
			}
			state := "proposed (needs a judgment)"
			if d.Settled {
				state = "settled (outside the harness)"
			}
			if d.Replayed {
				state += " · already derived"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "change %s ← commit %.8s: %d file(s), %d anchor(s), %d constraint(s), %d session(s) — %s\n",
				d.ChangeID, d.SHA, len(d.Files), len(d.Anchors), len(d.Constraints), len(d.Sessions), state)
			for _, a := range d.Anchors {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  anchor %s\n", a)
			}
			for _, id := range d.Constraints {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  constraint %s\n", id)
			}
			return nil
		},
	}
	derive.Flags().StringVar(&commit, "commit", "HEAD", "commit to derive from")
	derive.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	derive.Flags().BoolVar(&noVerify, "no-verify", false, "skip anchor verification at the commit")
	list := &cobra.Command{
		Use:   "list",
		Short: "List changes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			h, err := openHeadless(ctx, repoDir)
			if err != nil {
				return err
			}
			defer h.close(ctx)
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "ID\tSTATE\tMODE\tREF\tANCHORS\tCONSTRAINTS\tSESSIONS\tOPENED")
			for _, c := range h.changes.All() {
				ref := ""
				if len(c.Refs) > 0 {
					ref = short(c.Refs[0])
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n", c.ID, c.State, c.Mode, ref, len(c.Anchors), len(c.Constraints), len(c.Sessions), c.OpenedAt.Local().Format("2006-01-02 15:04"))
			}
			return w.Flush()
		},
	}
	c.AddCommand(derive, list)
	return c
}

func short(s string) string {
	if len(s) > 12 && !strings.Contains(s, "://") {
		return s[:12]
	}
	return s
}

func shortScope(s string) string {
	if s == "" {
		return ""
	}
	var parts []string
	for _, axis := range strings.Split(s, "|") {
		if k, v, ok := strings.Cut(axis, "="); ok && v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	if len(parts) == 0 {
		return "global"
	}
	return strings.Join(parts, ",")
}

var _ = time.Now
