package main

import (
	"fmt"
	"os"

	"github.com/SigNoz/signoz/pkg/query-service/vigil"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/audit"
	"github.com/spf13/cobra"
)

// auditCmd verifies the tamper-evident decision ledger.
//
// It reads the file directly rather than asking the server, which is the whole
// point: a tamper check routed through the process that writes the log proves
// nothing. It also means the chain can be verified after an incident, on a
// copied file, with the server long gone.
func auditCmd() *cobra.Command {
	var file string
	var quiet bool

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Inspect the tamper-evident decision ledger",
	}

	verify := &cobra.Command{
		Use:   "verify [session_id]",
		Short: "Verify the ledger hash chain",
		Long: `Verify walks the decision ledger and reports the first break in its
hash chain. Editing, deleting, or reordering any record is detected.

An optional session_id scopes the reported count to one session. Integrity is
always checked across the whole chain, since the chain links every session.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := file
			if path == "" {
				path = vigil.EnvOr("AUDIT_PATH", audit.DefaultPath)
			}

			var session string
			if len(args) == 1 {
				session = args[0]
			}

			rep, err := audit.VerifyFile(path, session)
			if err != nil {
				return fmt.Errorf("reading ledger %s: %w", path, err)
			}

			scope := ""
			if session != "" {
				scope = fmt.Sprintf(" for session %s", session)
			}

			if rep.OK {
				if !quiet {
					fmt.Printf("PASS — %d events verified%s\n", rep.Count, scope)
				}
				return nil
			}

			fmt.Fprintf(os.Stderr, "FAIL — tampering detected at event %d (%s)%s\n", rep.FailedAt, rep.Reason, scope)
			// Non-zero exit so this is usable as a CI or cron gate.
			os.Exit(1)
			return nil
		},
	}
	verify.Flags().StringVar(&file, "file", "", "path to the ledger (default: $VIGIL_AUDIT_PATH, else "+audit.DefaultPath+")")
	verify.Flags().BoolVarP(&quiet, "quiet", "q", false, "print nothing on success; rely on the exit code")

	cmd.AddCommand(verify)
	return cmd
}
