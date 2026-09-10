package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/adapter/claudecode"
	mcpsrv "github.com/young1ll/keelage/internal/adapter/mcp"
	"github.com/young1ll/keelage/internal/adapter/uds"
	"github.com/young1ll/keelage/internal/app"
)

func adapterCmd() *cobra.Command {
	c := &cobra.Command{Use: "adapter", Short: "Tool adapters: versions and installable artifacts"}
	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List adapters",
		Run: func(cmd *cobra.Command, _ []string) {
			_, _ = fmt.Fprint(cmd.OutOrStdout(), claudecode.Version())
		},
	})
	cc := &cobra.Command{Use: "claude-code", Short: "Claude Code adapter (hooks + local MCP + skill)"}
	cc.AddCommand(&cobra.Command{
		Use:   "print <hooks|skill|mcp|version>",
		Short: "Print an installable artifact: the settings.json hooks snippet, the SKILL.md, the MCP registration command, or VERSION",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := cmd.OutOrStdout()
			switch args[0] {
			case "hooks":
				_, _ = fmt.Fprint(w, claudecode.HooksSettings())
			case "skill":
				_, _ = fmt.Fprint(w, claudecode.Skill())
			case "mcp":
				_, _ = fmt.Fprintln(w, claudecode.MCPCommand())
			case "version":
				_, _ = fmt.Fprint(w, claudecode.Version())
			default:
				return fmt.Errorf("unknown artifact %q", args[0])
			}
			return nil
		},
	})
	c.AddCommand(cc)
	gitc := &cobra.Command{Use: "git", Short: "git hook (post-commit → change derive)"}
	gitc.AddCommand(&cobra.Command{
		Use:   "print post-commit",
		Short: "Print the post-commit hook script (install as .git/hooks/post-commit, mode 0755)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "post-commit" {
				return fmt.Errorf("unknown hook %q", args[0])
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), postCommitHook)
			return nil
		},
	})
	c.AddCommand(gitc)
	return c
}

// postCommitHook derives the Change for the commit just made. It never
// fails the commit (the commit is already done) and stays quiet on success.
const postCommitHook = `#!/bin/sh
# keelage: derive the Change for this commit and verify the anchors it touched.
command -v keelage >/dev/null 2>&1 || exit 0
keelage change derive --commit HEAD >/dev/null 2>&1 || true
`

func mcpCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "mcp",
		Short: "Run the local MCP server over stdio (register with 'keelage adapter claude-code print mcp')",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if socket == "" {
				home, err := resolveHome()
				if err != nil {
					return err
				}
				socket = filepath.Join(home, "keelage.sock")
			}
			repo := ""
			if dir := os.Getenv("CLAUDE_PROJECT_DIR"); dir != "" {
				repo = app.RepoID(dir)
			}
			srv := mcpsrv.New(uds.NewClient(socket, 2*time.Second), uds.BaseURL, repo, version)
			return srv.Run(cmd.Context())
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "Unix socket path (default ~/.keelage/keelage.sock)")
	return c
}
