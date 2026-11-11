package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/nmutto/healthcli/internal/importer"
	"github.com/spf13/cobra"
)

// importFlags are the options of `import apple`.
type importFlags struct {
	dryRun   bool
	noBackup bool
}

func newImportAppleCmd(opts *options) *cobra.Command {
	var flags importFlags
	cmd := &cobra.Command{
		Use:   "apple <export.zip|export.xml>",
		Short: "Import an Apple Health export",
		Long: `Import an Apple Health export file. In the Health app on your iPhone choose
your profile picture, then "Export All Health Data", and pass the resulting file here.

The file is streamed, so even multi-gigabyte exports use little memory.
Importing the same file again adds nothing new. A record whose key (type,
times, source) is already stored is not imported again; if its value differs
from the stored one it is counted as a conflict, and the stored value stays.

Before the first write into a database that already holds data, a safety copy
is made next to it (<database>.bak, readable only by you, replaced on every
import). Use --no-backup to skip it, for example when the database is huge
and you have your own backups.

--dry-run reads the whole file and prints the counts a real import would give,
but writes nothing (and makes no backup).

Press Ctrl-C to stop an import: the rows read so far stay saved, and running
the same import again finishes it. A byte that is not valid UTF-8 is replaced
with U+FFFD and counted instead of stopping the import.`,
		Example: `  healthcli import apple export.zip
  healthcli import apple export.zip --dry-run
  healthcli import apple export.xml --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImportApple(cmd, opts, flags, args[0])
		},
	}
	cmd.Flags().BoolVar(&flags.dryRun, "dry-run", false, "read and count everything, write nothing")
	cmd.Flags().BoolVar(&flags.noBackup, "no-backup", false, "do not copy the database to <database>.bak before importing")
	return cmd
}

func runImportApple(cmd *cobra.Command, opts *options, flags importFlags, file string) error {
	// Look at the input first: a typo in the path must not create a database.
	if err := importer.Check(file); err != nil {
		return err
	}
	path, err := dbPath(opts)
	if err != nil {
		return err
	}
	_, statErr := os.Stat(path)
	isNew := path != ":memory:" && errors.Is(statErr, os.ErrNotExist)

	conn, err := openOrCreateDB(opts)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			conn.Close()
		}
	}()

	ctx := cmdContext(cmd)
	var backup string
	if !flags.dryRun && !flags.noBackup && path != ":memory:" {
		if backup, err = importer.Backup(ctx, conn, path); err != nil {
			return err
		}
	}

	var iopts importer.Options
	iopts.DryRun = flags.dryRun
	// Progress is for humans: only show it when stderr is a terminal, so
	// pipes, logs and --json consumers never see it.
	if stderrIsTerminal() {
		iopts.Progress = func(read int64) {
			fmt.Fprintf(cmd.ErrOrStderr(), "\rread %d records...", read)
		}
	}

	sum, err := importer.ImportContext(ctx, conn, file, iopts)
	if iopts.Progress != nil {
		fmt.Fprint(cmd.ErrOrStderr(), "\r\033[K") // erase the progress line
	}

	// A database this command just created, with nothing in it (the import
	// failed, was stopped at once, or was a dry run), is not worth keeping.
	if isNew && (err != nil || sum.DryRun || sum.Interrupted) {
		// Not ctx: after Ctrl-C it is cancelled and the question would fail.
		// If we cannot tell, keep the file.
		if has, err := importer.HasData(context.Background(), conn); err == nil && !has {
			conn.Close()
			closed = true
			for _, suffix := range []string{"", "-wal", "-shm"} {
				os.Remove(path + suffix)
			}
		}
	}
	if err != nil {
		return err
	}
	sum.Backup = backup

	if err := printImportSummary(cmd.OutOrStdout(), sum, opts.jsonOutput); err != nil {
		return err
	}
	if sum.Interrupted {
		// Exit non-zero so scripts see that the file was not fully read.
		return fmt.Errorf("import interrupted after %d records; the rows read so far are saved, run the same import again to finish it (re-importing is safe)", sum.Read)
	}
	return nil
}

// printImportSummary writes the result as JSON or as text.
func printImportSummary(out io.Writer, sum *importer.Summary, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(sum)
	}
	if sum.DryRun {
		fmt.Fprintln(out, "Dry run: nothing was written. These are the counts a real import would give.")
	}
	fmt.Fprintf(out, "Read:       %d records\n", sum.Read)
	fmt.Fprintf(out, "Inserted:   %d\n", sum.Inserted)
	fmt.Fprintf(out, "Duplicates: %d (same key and value as a row already stored or earlier in the file)\n", sum.Duplicates)
	if sum.Conflicts > 0 {
		fmt.Fprintf(out, "Conflicts:  %d (same key, different value: the stored one was kept)\n", sum.Conflicts)
	}
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
	if sum.InvalidBytes > 0 {
		fmt.Fprintf(out, "Invalid bytes: %d (not valid UTF-8, replaced with U+FFFD)\n", sum.InvalidBytes)
	}
	if sum.Backup != "" {
		fmt.Fprintf(out, "Backup:     %s\n", sum.Backup)
	}
	fmt.Fprintf(out, "Elapsed:    %.1fs\n", sum.ElapsedSec)
	return nil
}

// stderrIsTerminal reports whether stderr is an interactive terminal.
func stderrIsTerminal() bool {
	st, err := os.Stderr.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
