package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/adapter/claudecode"
	"github.com/young1ll/keelage/internal/app"
	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/realization"
)

// claudeDir is ~/.claude, or $CLAUDE_CONFIG_DIR.
func claudeDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

func claudeSettingsPath() string { return filepath.Join(claudeDir(), "settings.json") }

// setupState is what `keelage setup` did, so `uninstall` can undo exactly that.
type setupState struct {
	At              time.Time `json:"at"`
	SettingsPath    string    `json:"settings_path"`
	SettingsBackup  string    `json:"settings_backup,omitempty"`
	SettingsExisted bool      `json:"settings_existed"`
	// SettingsWritten is the hash of the file as setup left it: when the
	// user has not touched it since, uninstall restores the backup byte
	// for byte instead of re-serialising.
	SettingsWritten string `json:"settings_written,omitempty"`
	HooksInstalled  bool   `json:"hooks_installed"`
	MCP             string `json:"mcp"` // cli | manual | skipped
	GlobalRulesFile string `json:"global_rules_file,omitempty"`
}

func setupStatePath(home string) string { return filepath.Join(home, "setup.json") }

func loadSetupState(home string) (*setupState, error) {
	b, err := os.ReadFile(setupStatePath(home))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s setupState
	return &s, json.Unmarshal(b, &s)
}

func saveSetupState(home string, s *setupState) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(setupStatePath(home), append(b, '\n'), 0o600)
}

func supplyFileHash(b []byte) string { return realization.FileHash(b) }

// backup copies path into ~/.keelage/backup/<timestamp>/ and returns the copy.
func backup(home, path string) (string, error) {
	cur, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "backup", time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, filepath.Base(path))
	if err := os.WriteFile(dst, cur, 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

func mcpRegistered() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return false
	}
	var doc struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return false
	}
	_, ok := doc.MCPServers["keelage"]
	return ok
}

func setupCmd() *cobra.Command {
	var yes, dryRun, skipMCP bool
	c := &cobra.Command{
		Use:   "setup",
		Short: "Global, once: install the Claude Code hooks (backup kept), register the MCP server, absorb ~/.claude/CLAUDE.md",
		Long: `Each item is shown and approved separately. Only keelage's own entries are added to
~/.claude/settings.json; existing hooks stay. The original file is copied to
~/.keelage/backup/. Global rules are absorbed into the personal layer; the file
itself is not rewritten. Observe mode starts immediately: every repository's edits
reach the personal ledger, nothing is written into repositories until 'keelage init'.
Daemon autostart is not automated in v0: see the printed instructions.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			rt, err := openCore(ctx)
			if err != nil {
				return err
			}
			defer rt.close()
			state, _ := loadSetupState(rt.home)
			if state == nil {
				state = &setupState{At: time.Now(), SettingsPath: claudeSettingsPath(), MCP: "skipped"}
			}

			// 1. hooks
			settings, err := os.ReadFile(claudeSettingsPath())
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			merged, changed, err := claudecode.MergeHooks(settings)
			if err != nil {
				return err
			}
			switch {
			case !changed:
				_, _ = fmt.Fprintln(out, "hooks    already installed in", claudeSettingsPath())
			case dryRun:
				_, _ = fmt.Fprintln(out, "hooks    would add keelage handlers to", claudeSettingsPath())
			case approve(cmd, yes, "Add keelage's hook handlers to "+claudeSettingsPath()+" (existing entries kept, original backed up)?"):
				bk, err := backup(rt.home, claudeSettingsPath())
				if err != nil {
					return err
				}
				if err := os.MkdirAll(claudeDir(), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(claudeSettingsPath(), merged, 0o644); err != nil {
					return err
				}
				state.SettingsBackup, state.HooksInstalled = bk, true
				state.SettingsExisted = settings != nil
				state.SettingsWritten = supplyFileHash(merged)
				_, _ = fmt.Fprintf(out, "hooks    installed (backup: %s)\n", bk)
			default:
				_, _ = fmt.Fprintln(out, "hooks    skipped")
			}

			// 2. MCP
			switch {
			case skipMCP:
				_, _ = fmt.Fprintln(out, "mcp      skipped")
			case mcpRegistered():
				_, _ = fmt.Fprintln(out, "mcp      already registered")
				if state.MCP == "skipped" {
					state.MCP = "manual"
				}
			case dryRun:
				_, _ = fmt.Fprintln(out, "mcp      would register:", claudecode.MCPCommand())
			case approve(cmd, yes, "Register the keelage MCP server with Claude Code ("+claudecode.MCPCommand()+")?"):
				if _, err := exec.LookPath("claude"); err == nil {
					c := exec.CommandContext(ctx, "claude", "mcp", "add", "--transport", "stdio", "--scope", "user", "keelage", "--", "keelage", "mcp")
					if b, err := c.CombinedOutput(); err != nil {
						_, _ = fmt.Fprintf(out, "mcp      claude CLI failed (%v): %s\n         run manually: %s\n", err, string(b), claudecode.MCPCommand())
						state.MCP = "manual"
					} else {
						state.MCP = "cli"
						_, _ = fmt.Fprintln(out, "mcp      registered via claude CLI")
					}
				} else {
					state.MCP = "manual"
					_, _ = fmt.Fprintln(out, "mcp      claude CLI not found — run:", claudecode.MCPCommand())
				}
			default:
				_, _ = fmt.Fprintln(out, "mcp      skipped")
			}

			// 3. global rules → personal layer
			globalRules := filepath.Join(claudeDir(), "CLAUDE.md")
			if _, err := os.Stat(globalRules); err == nil {
				if dryRun {
					_, _ = fmt.Fprintln(out, "rules    would absorb", globalRules, "into the personal layer")
				} else {
					m, err := app.LoadManifest(rt.home, "personal")
					if err != nil {
						return err
					}
					person := core.ScopeKey{Person: string(localUser())}
					im := &app.Importer{Pipeline: rt.pipeline, IDs: rt.ids, Constraints: rt.constraints, Decisions: rt.decisions, Root: claudeDir(), Repo: "personal", Clock: clock{}, OnlyFiles: []string{"CLAUDE.md"}, Scope: &person}
					actor := core.ActorRef{Kind: core.ActorHuman, ID: localUser()}
					results, err := im.Absorb(ctx, actor, m, true)
					if err != nil {
						return err
					}
					for _, r := range results {
						_, _ = fmt.Fprintf(out, "rules    %s %s → %s (personal layer; the file is not rewritten)\n", r.Action, globalRules, r.ObjectID)
					}
					if err := app.SaveManifest(rt.home, m, time.Now()); err != nil {
						return err
					}
					state.GlobalRulesFile = globalRules
				}
			} else {
				_, _ = fmt.Fprintln(out, "rules    no", globalRules)
			}

			// 4. autostart: instructions only
			_, _ = fmt.Fprintf(out, "daemon   start with `keelage daemon` (autostart is manual in v0: a systemd user unit or launchd agent running `keelage daemon`)\n")
			if dryRun {
				return nil
			}
			if err := saveSetupState(rt.home, state); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(out, "observe mode is on for every repository; run `keelage init` in a repository to activate it")
			return nil
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "apply every item without asking")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change")
	c.Flags().BoolVar(&skipMCP, "skip-mcp", false, "do not register the MCP server")
	return c
}

func uninstallCmd() *cobra.Command {
	var yes, purge bool
	c := &cobra.Command{
		Use:   "uninstall",
		Short: "Undo setup and every init: remove hook entries, MCP registration, managed blocks and sidecars. The ledger stays unless --purge",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			home, err := resolveHome()
			if err != nil {
				return err
			}
			if !approve(cmd, yes, "Remove keelage's hook entries, MCP registration, and every managed block/sidecar it wrote?") {
				_, _ = fmt.Fprintln(out, "aborted")
				return nil
			}
			repos, err := loadRepos(home)
			if err != nil {
				return err
			}
			for _, root := range repos {
				if err := uninitRepo(cmd, home, root, true); err != nil {
					return err
				}
			}
			state, _ := loadSetupState(home)
			settings, err := os.ReadFile(claudeSettingsPath())
			if err == nil {
				switch {
				case state != nil && state.HooksInstalled && supplyFileHash(settings) == state.SettingsWritten && !state.SettingsExisted:
					// there was no settings.json before setup
					if err := os.Remove(claudeSettingsPath()); err != nil {
						return err
					}
					_, _ = fmt.Fprintln(out, "hooks    removed;", claudeSettingsPath(), "did not exist before setup")
				case state != nil && state.HooksInstalled && supplyFileHash(settings) == state.SettingsWritten && state.SettingsBackup != "":
					// untouched since setup: restore the original bytes
					orig, err := os.ReadFile(state.SettingsBackup)
					if err != nil {
						return err
					}
					if err := os.WriteFile(claudeSettingsPath(), orig, 0o644); err != nil {
						return err
					}
					_, _ = fmt.Fprintln(out, "hooks    restored", claudeSettingsPath(), "from", state.SettingsBackup)
				default:
					// edited since setup: take only our handlers out
					restored, changed, err := claudecode.RemoveHooks(settings)
					if err != nil {
						return err
					}
					if changed {
						if _, err := backup(home, claudeSettingsPath()); err != nil {
							return err
						}
						if err := os.WriteFile(claudeSettingsPath(), restored, 0o644); err != nil {
							return err
						}
						_, _ = fmt.Fprintln(out, "hooks    removed from", claudeSettingsPath())
					}
				}
			}
			if state != nil && state.MCP == "cli" {
				if _, err := exec.LookPath("claude"); err == nil {
					c := exec.CommandContext(ctx, "claude", "mcp", "remove", "--scope", "user", "keelage")
					if b, err := c.CombinedOutput(); err != nil {
						_, _ = fmt.Fprintf(out, "mcp      claude CLI failed (%v): %s\n", err, string(b))
					} else {
						_, _ = fmt.Fprintln(out, "mcp      unregistered")
					}
				}
			} else if mcpRegistered() {
				_, _ = fmt.Fprintln(out, "mcp      still registered — run: claude mcp remove --scope user keelage")
			}
			_ = os.Remove(setupStatePath(home))
			if purge {
				if err := os.RemoveAll(home); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(out, "purged", home)
				return nil
			}
			_, _ = fmt.Fprintf(out, "kept %s (ledger, backups); use --purge to delete it\n", home)
			return nil
		},
	}
	c.Flags().BoolVar(&yes, "yes", false, "do not ask for approval")
	c.Flags().BoolVar(&purge, "purge", false, "also delete ~/.keelage (the personal ledger)")
	return c
}
