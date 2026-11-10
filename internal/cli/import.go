package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/nmutto/healthcli/internal/importer"
	"github.com/spf13/cobra"
)

func runImportApple(cmd *cobra.Command, opts *options, file string) error {
	conn, err := openOrCreateDB(opts)
	if err != nil {
		return err
	}
	defer conn.Close()

	var iopts importer.Options
	// Progress is for humans: only show it when stderr is a terminal, so
	// pipes, logs and --json consumers never see it.
	if stderrIsTerminal() {
		iopts.Progress = func(read int64) {
			fmt.Fprintf(cmd.ErrOrStderr(), "\rread %d records...", read)
		}
	}

	sum, err := importer.Import(conn, file, iopts)
	if iopts.Progress != nil {
		fmt.Fprint(cmd.ErrOrStderr(), "\r\033[K") // erase the progress line
	}
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if opts.jsonOutput {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(sum)
	}
	fmt.Fprintf(out, "Read:       %d records\n", sum.Read)
	fmt.Fprintf(out, "Inserted:   %d\n", sum.Inserted)
	fmt.Fprintf(out, "Duplicates: %d (already in the database)\n", sum.Duplicates)
	fmt.Fprintf(out, "Skipped:    %d\n", sum.SkippedTotal())
	reasons := make([]string, 0, len(sum.Skipped))
	for r := range sum.Skipped {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons) // maps iterate in random order
	for _, r := range reasons {
		fmt.Fprintf(out, "  %-20s %d\n", r, sum.Skipped[r])
	}
	if top := sum.TopSkippedTypes(5); len(top) > 0 {
		fmt.Fprintln(out, "Most common types not imported:")
		for _, t := range top {
			fmt.Fprintf(out, "  %-50s %d\n", t.Type, t.Count)
		}
	}
	fmt.Fprintf(out, "Elapsed:    %.1fs\n", sum.ElapsedSec)
	return nil
}

// stderrIsTerminal reports whether stderr is an interactive terminal.
func stderrIsTerminal() bool {
	st, err := os.Stderr.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
