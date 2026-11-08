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

	importCmd.AddCommand(&cobra.Command{
		Use:   "apple <export.zip|export.xml>",
		Short: "Import an Apple Health export",
		Long: `Import an Apple Health export file. In the Health app on your iPhone choose
your profile picture, then "Export All Health Data", and pass the resulting file here.

The file is streamed, so even multi-gigabyte exports use little memory.
Importing the same file again adds nothing new.`,
		Example: `  healthcli import apple export.zip
  healthcli import apple export.xml --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImportApple(cmd, opts, args[0])
		},
	})
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
