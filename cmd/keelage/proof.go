package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/young1ll/keelage/internal/adapter/keys"
	"github.com/young1ll/keelage/internal/merkle"
)

// verify-proof is the public, server-less verifier (ADR 0004): an auditor
// checks a record's inclusion in a signed checkpoint, or that a later
// checkpoint extends an earlier one, with only the server's public key.
func verifyProofCmd() *cobra.Command {
	var pubKey, file string
	c := &cobra.Command{
		Use:   "verify-proof",
		Short: "Verify an inclusion or consistency proof bundle offline against the server's public key",
		Long: `Reads a bundle (JSON from GET /ledger/proof/inclusion?seq= or
/ledger/proof/consistency?from=&to=) from --file or stdin and verifies the
checkpoint signature, the leaf hash and the Merkle proof. Exit 0 = valid.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pub, err := keys.DecodePublic(pubKey)
			if err != nil {
				return err
			}
			r := cmd.InOrStdin()
			if file != "" {
				f, err := os.Open(file)
				if err != nil {
					return err
				}
				defer func() { _ = f.Close() }()
				r = f
			}
			raw, err := io.ReadAll(io.LimitReader(r, 4<<20))
			if err != nil {
				return err
			}
			var probe struct {
				From *merkle.Checkpoint `json:"from"`
				Seq  *int64             `json:"seq"`
			}
			if err := json.Unmarshal(raw, &probe); err != nil {
				return fmt.Errorf("bundle: %w", err)
			}
			if probe.From != nil {
				var b merkle.ConsistencyBundle
				if err := json.Unmarshal(raw, &b); err != nil {
					return err
				}
				if err := merkle.VerifyConsistencyBundle(b, pub); err != nil {
					return fmt.Errorf("INVALID: %w", err)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "VALID consistency %s: size %d (%s) → size %d (%s), key %s\n", b.Org, b.From.TreeSize, b.From.At.Format("2006-01-02T15:04:05Z"), b.To.TreeSize, b.To.At.Format("2006-01-02T15:04:05Z"), b.To.KeyID)
				return nil
			}
			var b merkle.Bundle
			if err := json.Unmarshal(raw, &b); err != nil {
				return err
			}
			if err := merkle.VerifyBundle(b, pub); err != nil {
				return fmt.Errorf("INVALID: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "VALID inclusion %s seq %d in tree of %d (checkpoint %s, key %s)\n", b.Org, b.Seq, b.TreeSize, b.Checkpoint.At.Format("2006-01-02T15:04:05Z"), b.Checkpoint.KeyID)
			return nil
		},
	}
	c.Flags().StringVar(&pubKey, "server-key", "", "server public key (ed25519:base64)")
	c.Flags().StringVar(&file, "file", "", "bundle file (default: stdin)")
	_ = c.MarkFlagRequired("server-key")
	return c
}
