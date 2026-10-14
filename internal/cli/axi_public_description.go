package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	toon "github.com/toon-format/toon-go"

	"github.com/kunchenguid/no-mistakes/internal/git"
	"github.com/kunchenguid/no-mistakes/internal/ipc"
	"github.com/kunchenguid/no-mistakes/internal/publicdescription"
)

// A separate boundary-only operation lets an existing driver keep the SAME
// parked run and its original acceptance intent. This is not a gate response.
func newAxiPublicDescriptionCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use: "public-description", Short: "Pin final public material on the current branch's parked pre-push run",
		Long: "Capture independent committable JSON {title,body} once for the existing\n" +
			"active run. The current repository/branch selects the run; no --run.\n" +
			"Requires a parked pre-push gate on a compatible daemon. Does not edit\n" +
			"source, change private intent, resolve the gate, restart validation or\n" +
			"publish anything. Continue with the existing owner's normal response.\n" +
			"Replacement and adoption after publication are refused.",
		Args: cobra.NoArgs, SilenceErrors: true, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if file == "" {
				return emitError(cmd, 2, "--file is required")
			}
			raw, err := publicdescription.ReadFile(file)
			if err != nil {
				return emitError(cmd, 2, err.Error())
			}
			env, err := openAxiDaemonEnv()
			if err != nil {
				return emitError(cmd, 1, err.Error(), repoInitHelp(err)...)
			}
			defer env.close()
			branch, err := git.CurrentBranch(cmd.Context(), ".")
			if err != nil {
				return emitError(cmd, 1, err.Error())
			}
			var active ipc.GetActiveRunResult
			if err := env.client.Call(ipc.MethodGetActiveRun, activeRunLookupParams(env.repo.ID, branch), &active); err != nil {
				return emitError(cmd, 1, err.Error())
			}
			if active.Run == nil || active.Run.SubmittedHeadSHA == nil {
				return emitError(cmd, 2, "no existing active run with a submitted head")
			}
			var result ipc.ProbeOmitIntentResult
			// This distinct RPC is the compatibility check. An older runtime
			// rejects it rather than ignoring an added field in a legacy call.
			if err := env.client.Call(ipc.MethodAdoptPublicDescription, &ipc.AdoptPublicDescriptionParams{
				RepoID: env.repo.ID, Branch: branch, RunID: active.Run.ID,
				SubmittedHeadSHA: *active.Run.SubmittedHeadSHA, Description: raw,
			}, &result); err != nil {
				return emitError(cmd, 2, fmt.Sprintf("adopt public description (requires compatible runtime): %v", err))
			}
			if !result.OK {
				return emitError(cmd, 2, "public description adoption not acknowledged")
			}
			fmt.Fprint(cmd.OutOrStdout(), axiDoc(toon.Field{Key: "pinned", Value: true}, toon.Field{Key: "run", Value: active.Run.ID}, toon.Field{Key: "gate_resumed", Value: false}))
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "independent final public JSON file (required; captured bytes immutable, source file untouched)")
	return cmd
}
