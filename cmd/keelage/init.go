package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/adapter/claudecode"
	"github.com/young1ll/keelage/internal/app"
	"github.com/young1ll/keelage/internal/core/supply"
)

// approve asks y/N on the command's stdin unless yes is set.
func approve(cmd *cobra.Command, yes bool, question string) bool {
	if yes {
		return true
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N] ", question)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

// registered repositories (for uninstall): ~/.keelage/repos.json
func reposFile(home string) string { return filepath.Join(home, "repos.json") }

func loadRepos(home string) ([]string, error) {
	b, err := os.ReadFile(reposFile(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	return out, json.Unmarshal(b, &out)
}

func saveRepos(home string, repos []string) error {
	sort.Strings(repos)
	b, _ := json.MarshalIndent(repos, "", "  ")
	return os.WriteFile(reposFile(home), append(b, '\n'), 0o600)
}

func registerRepo(home, root string) error {
	repos, err := loadRepos(home)
	if err != nil {
		return err
	}
	for _, r := range repos {
		if r == root {
			return nil
		}
	}
	return saveRepos(home, append(repos, root))
}

func unregisterRepo(home, root string) error {
	repos, err := loadRepos(home)
	if err != nil {
		return err
	}
	var kept []string
	for _, r := range repos {
		if r != root {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		err := os.Remove(reposFile(home))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return saveRepos(home, kept)
}

func initCmd() *cobra.Command {
	var repoDir string
	var verify, force, dryRun, yes bool
	c := &cobra.Command{
		Use:   "init",
		Short: "Activate keelage in this repository: absorb rule files and ADRs, render the context, write the sidecar",
		Long: `Absorbs CLAUDE.md / AGENTS.md (root and nested) as rule constraints and docs/adr/*.md
as decisions, renders the charter in force into .keelage/context/claude-code/context.md,
and puts one managed block (an @import line) into CLAUDE.md. Text outside the block is
never touched; a block you edited is reported as diverged and left alone unless --force.
Re-running is idempotent; changed files supersede/revise their objects.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			h, err := openHeadless(ctx, repoDir)
			if err != nil {
				return err
			}
			defer h.close(ctx)
			root := h.repo.Root
			repo := app.RepoID(root)
			m, err := app.LoadManifest(root, repo)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			im := &app.Importer{Pipeline: h.pipeline, IDs: h.ids, Constraints: h.constraints, Decisions: h.decisions, Root: root, Repo: repo, Clock: clock{}}
			if dryRun {
				_, _ = fmt.Fprintln(out, "dry run: nothing is written")
			}
			if !dryRun && !approve(cmd, yes, fmt.Sprintf("Absorb rule files and ADRs of %s into the ledger and render the context (CLAUDE.md managed block, .keelage/)?", repo)) {
				_, _ = fmt.Fprintln(out, "aborted")
				return nil
			}
			if !dryRun {
				results, err := im.Absorb(ctx, h.actor, m, verify)
				if err != nil {
					return err
				}
				for _, r := range results {
					_, _ = fmt.Fprintf(out, "absorb  %-9s %-4s %s → %s\n", r.Action, r.Kind, r.Path, r.ObjectID)
				}
			}
			r := &app.Renderer{Constraints: h.constraints, Decisions: h.decisions, Anchors: h.anchors, Repo: repo, Root: root, Tool: claudecode.Name, Version: adapterVersion(), Person: string(localUser())}
			_, ops := r.Plan(m)
			if dryRun {
				for _, op := range ops {
					kind := "write"
					if op.Managed {
						kind = "block"
					}
					_, _ = fmt.Fprintf(out, "render  %-9s %s\n", kind, op.Path)
				}
				return nil
			}
			applied, err := r.Apply(m, ops, force)
			if err != nil {
				return err
			}
			for _, a := range applied {
				_, _ = fmt.Fprintf(out, "render  %-9s %s\n", a.Action, a.Path)
			}
			if added, err := app.EnsureGitignore(root, m); err != nil {
				return err
			} else if added {
				_, _ = fmt.Fprintln(out, "gitignore .keelage/context/ added")
			}
			if err := app.SaveManifest(root, m, time.Now()); err != nil {
				return err
			}
			if err := registerRepo(h.home, root); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "sidecar %s written\n", supply.ManifestPath)
			return nil
		},
	}
	c.Flags().StringVar(&repoDir, "repo", ".", "repository directory")
	c.Flags().BoolVar(&verify, "verify", false, "mark imported objects verified right away (imported-verified)")
	c.Flags().BoolVar(&force, "force", false, "overwrite diverged managed blocks")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change")
	c.Flags().BoolVar(&yes, "yes", false, "do not ask for approval")
	return c
}

func uninitCmd() *cobra.Command {
	var repoDir string
	var yes bool
	c := &cobra.Command{
		Use:   "uninit",
		Short: "Deactivate keelage in this repository: remove managed blocks, rendered files, the .gitignore line and the sidecar",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			h, err := openHeadless(ctx, repoDir)
			if err != nil {
				return err
			}
			defer h.close(ctx)
			return uninitRepo(cmd, h.home, h.repo.Root, yes)
		},
	}
	c.Flags().StringVar(&repoDir, "repo", ".", "repository directory")
	c.Flags().BoolVar(&yes, "yes", false, "do not ask for approval")
	return c
}

func uninitRepo(cmd *cobra.Command, home, root string, yes bool) error {
	if !app.ManifestExists(root) {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: not initialised\n", root)
		return unregisterRepo(home, root)
	}
	m, err := app.LoadManifest(root, app.RepoID(root))
	if err != nil {
		return err
	}
	if !approve(cmd, yes, fmt.Sprintf("Remove keelage's managed block, rendered files and sidecar from %s? (the ledger keeps its records)", root)) {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "aborted")
		return nil
	}
	results, err := app.Uninit(root, m)
	if err != nil {
		return err
	}
	for _, r := range results {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-9s %s\n", r.Action, r.Path)
	}
	return unregisterRepo(home, root)
}

func statusCmd() *cobra.Command {
	var repoDir string
	c := &cobra.Command{
		Use:   "status",
		Short: "Where keelage stands: daemon, hooks, MCP, this repository's sidecar and divergence",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			home, err := resolveHome()
			if err != nil {
				return err
			}
			sock := filepath.Join(home, "keelage.sock")
			if conn, err := net.DialTimeout("unix", sock, 200*time.Millisecond); err == nil {
				_ = conn.Close()
				_, _ = fmt.Fprintf(out, "daemon   running (%s)\n", sock)
			} else {
				_, _ = fmt.Fprintf(out, "daemon   not running (%s) — hooks fail open\n", sock)
			}
			settings, _ := os.ReadFile(claudeSettingsPath())
			if claudecode.HasHooks(settings) {
				_, _ = fmt.Fprintln(out, "hooks    installed in", claudeSettingsPath())
			} else {
				_, _ = fmt.Fprintln(out, "hooks    not installed — run `keelage setup`")
			}
			if mcpRegistered() {
				_, _ = fmt.Fprintln(out, "mcp      keelage registered")
			} else {
				_, _ = fmt.Fprintln(out, "mcp      not registered —", claudecode.MCPCommand())
			}
			root, ok := app.FindRepoRoot(repoDir)
			if !ok {
				_, _ = fmt.Fprintln(out, "repo     not a git repository")
				return nil
			}
			if !app.ManifestExists(root) {
				_, _ = fmt.Fprintf(out, "repo     %s — observe mode (not initialised; run `keelage init`)\n", root)
				return nil
			}
			m, err := app.LoadManifest(root, app.RepoID(root))
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "repo     %s — active (%d import(s), %d rendered, adapters %v)\n", root, len(m.Imports), len(m.Rendered), m.Adapters)
			for _, rf := range m.Rendered {
				full := filepath.Join(root, filepath.FromSlash(rf.Path))
				cur, err := os.ReadFile(full)
				if err != nil {
					_, _ = fmt.Fprintf(out, "  %-9s %s\n", "missing", rf.Path)
					continue
				}
				if rf.Managed {
					hash, body, present := supply.FindBlock(string(cur), rf.BlockID)
					switch {
					case !present:
						_, _ = fmt.Fprintf(out, "  %-9s %s (managed block removed)\n", "missing", rf.Path)
					case hash != supply.BlockHash(body) || hash != rf.Hash:
						_, _ = fmt.Fprintf(out, "  %-9s %s (edit inside the managed block; `keelage init --force` overwrites)\n", "diverged", rf.Path)
					default:
						_, _ = fmt.Fprintf(out, "  %-9s %s\n", "ok", rf.Path)
					}
					continue
				}
				if supplyFileHash(cur) != rf.Hash {
					_, _ = fmt.Fprintf(out, "  %-9s %s (generated file edited; `keelage init` regenerates)\n", "diverged", rf.Path)
				} else {
					_, _ = fmt.Fprintf(out, "  %-9s %s\n", "ok", rf.Path)
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&repoDir, "repo", ".", "repository directory")
	return c
}

func adapterVersion() string {
	for _, l := range strings.Split(claudecode.Version(), "\n") {
		if v, ok := strings.CutPrefix(l, "version: "); ok {
			return strings.TrimSpace(v)
		}
	}
	return "unknown"
}
