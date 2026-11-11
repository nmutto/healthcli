package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// newImportCmd is a parent command: "import" itself does nothing, it only
// groups importers by source (apple now, maybe others later).
func newImportCmd(opts *options) *cobra.Command {
	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Import data from another source",
		Long:  "Import health data from an external source into the local database.",
	}

	importCmd.AddCommand(newImportAppleCmd(opts))
	return importCmd
}

func newVersionCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the healthcli version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{
					"version": Version, "commit": Commit, "date": Date,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "healthcli %s (commit %s, built %s)\n", Version, Commit, Date)
			return nil
		},
	}
}
