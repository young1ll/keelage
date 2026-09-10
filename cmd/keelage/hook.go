package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/adapter/claudecode"
	"github.com/young1ll/keelage/internal/adapter/uds"
	"github.com/young1ll/keelage/internal/core/supply"
)

// HookBudget is the whole budget of `keelage hook`: dial, request, answer.
// Past it the hook prints nothing and exits 0 — the edit is never blocked
// by keelage's absence (fail-open, CLAUDE.md rule).
const HookBudget = 50 * time.Millisecond

func debugf(format string, args ...any) {
	if os.Getenv("KEELAGE_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "keelage hook: "+format+"\n", args...)
	}
}

func hookCmd() *cobra.Command {
	var socket string
	c := &cobra.Command{
		Use:   "hook <tool> <event>",
		Short: "Tool hook entry point: stdin JSON → daemon → stdout JSON (fail-open)",
		Long: `Installed in the tool's hook settings (see 'keelage adapter claude-code print hooks').
Reads the tool's hook input on stdin, normalises it, asks the daemon over the
Unix socket within 50 ms and prints the tool-formatted answer. Any failure —
no daemon, timeout, bad input — prints nothing and exits 0.`,
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			runHook(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), args[0], args[1], socket)
			return nil
		},
	}
	c.Flags().StringVar(&socket, "socket", "", "Unix socket path (default ~/.keelage/keelage.sock)")
	return c
}

// runHook never fails: every error path returns silently after a debug line.
func runHook(ctx context.Context, stdin io.Reader, stdout io.Writer, tool, event, socket string) {
	if tool != claudecode.Name {
		debugf("unknown tool %q", tool)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		debugf("read stdin: %v", err)
		return
	}
	ev, handled, err := claudecode.Normalize(event, raw, time.Now())
	if err != nil {
		debugf("normalize: %v", err)
		return
	}
	if !handled {
		return
	}
	if socket == "" {
		home, err := resolveHome()
		if err != nil {
			debugf("home: %v", err)
			return
		}
		socket = filepath.Join(home, "keelage.sock")
	}
	res, ok := askDaemon(ctx, socket, ev)
	if !ok {
		return
	}
	out, err := claudecode.Render(event, res)
	if err != nil || out == nil {
		return
	}
	_, _ = stdout.Write(append(out, '\n'))
}

func askDaemon(ctx context.Context, socket string, ev supply.Event) (supply.Response, bool) {
	ctx, cancel := context.WithTimeout(ctx, HookBudget)
	defer cancel()
	body, err := json.Marshal(ev)
	if err != nil {
		return supply.Response{}, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uds.BaseURL+"/v1/hook", bytes.NewReader(body))
	if err != nil {
		return supply.Response{}, false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := uds.NewClient(socket, HookBudget).Do(req)
	if err != nil {
		debugf("daemon: %v (fail-open)", err)
		return supply.Response{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		debugf("daemon: %s (fail-open)", resp.Status)
		return supply.Response{}, false
	}
	var res supply.Response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&res); err != nil {
		debugf("daemon: decode: %v", err)
		return supply.Response{}, false
	}
	return res, true
}
