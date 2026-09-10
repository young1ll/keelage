package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/port"
)

// gateCmd is the thin client of the approval broker (spec §3.6a): an agent
// (agent token) asks before acting; a person answers an open gate; both
// answers are records on the server ledger.
func gateCmd() *cobra.Command {
	c := &cobra.Command{Use: "gate", Short: "Ask the team server before acting, and answer open gates"}
	var asJSON bool
	var scope, action, proposal, level string
	var anchors []string
	ask := &cobra.Command{
		Use:   "ask",
		Short: "Ask allow/deny/ask for a proposal (needs an agent token)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome()
			if err != nil {
				return err
			}
			client, _, err := teamClient(home)
			if err != nil {
				return err
			}
			if proposal == "" {
				return errors.New("--proposal is required")
			}
			key, err := core.ParseScopeKey(scope)
			if err != nil {
				return err
			}
			lvl, err := core.ParseLevel(level)
			if err != nil {
				return err
			}
			g, err := client.RequestGate(cmd.Context(), port.GateRequest{Scope: key, Anchors: anchors, Action: action, ProposalRef: proposal, RequestedLevel: lvl})
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(g)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s  policy=%s\n", g.ID, g.State, g.Decision, g.PolicyRef)
			return nil
		},
	}
	ask.Flags().StringVar(&scope, "scope", "", "canonical scope key (org=..|product=..|team=..|repo=..|path=..|person=..)")
	ask.Flags().StringVar(&action, "action", "", "what the agent wants to do")
	ask.Flags().StringVar(&proposal, "proposal", "", "proposal reference (idempotent per actor)")
	ask.Flags().StringVar(&level, "level", "L1", "requested autonomy level")
	ask.Flags().StringArrayVar(&anchors, "anchor", nil, "anchors the action touches")

	var allow, deny bool
	var reason string
	resolve := &cobra.Command{
		Use:   "resolve <gate-id>",
		Short: "Answer an open gate (--allow | --deny, with --reason)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := resolveHome()
			if err != nil {
				return err
			}
			client, _, err := teamClient(home)
			if err != nil {
				return err
			}
			if allow == deny {
				return errors.New("exactly one of --allow or --deny")
			}
			d := accountability.GateAllow
			if deny {
				d = accountability.GateDeny
			}
			g, err := client.ResolveGate(cmd.Context(), core.ID(args[0]), port.GateResolution{Decision: d, Reason: reason})
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(g)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s\n", g.ID, g.State, g.Decision)
			return nil
		},
	}
	resolve.Flags().BoolVar(&allow, "allow", false, "allow")
	resolve.Flags().BoolVar(&deny, "deny", false, "deny")
	resolve.Flags().StringVar(&reason, "reason", "", "why (required)")

	var state string
	list := &cobra.Command{
		Use:   "list",
		Short: "List gates (--state open|decided|resolved|expired)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := resolveHome()
			if err != nil {
				return err
			}
			client, _, err := teamClient(home)
			if err != nil {
				return err
			}
			gs, err := client.ListGates(cmd.Context(), accountability.GateState(state))
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(gs)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "GATE\tSTATE\tDECISION\tACTOR\tLEVEL\tPROPOSAL\tPOLICY")
			for _, g := range gs {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", g.ID, g.State, g.Decision, g.Actor.ID, g.RequestedLevel, g.ProposalRef, g.PolicyRef)
			}
			return w.Flush()
		},
	}
	list.Flags().StringVar(&state, "state", "", "filter by state")
	c.PersistentFlags().BoolVar(&asJSON, "json", false, "print as JSON")
	c.AddCommand(ask, resolve, list)
	return c
}
