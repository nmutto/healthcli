package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newQueryCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "query <sql>",
		Short: "Run a read-only SQL query",
		Long: `Run a read-only SQL query (SELECT or WITH) against the local database and print the rows.
Main table: samples(metric, start_ts, end_ts, value, unit, tz_offset, source).`,
		Example: `  health query "SELECT count(*) FROM samples"
  health query "SELECT * FROM samples LIMIT 5" --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQuery(cmd, opts, args[0])
		},
	}
}

func runQuery(cmd *cobra.Command, opts *options, query string) error {
	// First line of defence: a friendly error for anything that is not a read.
	if !looksReadOnly(query) {
		return fmt.Errorf("only SELECT queries are allowed")
	}

	conn, err := openDB(opts)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Second line of defence: SQLite itself refuses all writes on this
	// connection. Open uses a single-connection pool, so the setting sticks.
	if _, err := conn.Exec(`PRAGMA query_only = ON`); err != nil {
		return fmt.Errorf("make connection read-only: %w", err)
	}

	rows, err := conn.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()

	cols, data, err := readRows(rows)
	if err != nil {
		return err
	}
	if opts.jsonOutput {
		return writeJSON(cmd.OutOrStdout(), cols, data)
	}
	return writeTable(cmd.OutOrStdout(), cols, data)
}

// looksReadOnly accepts statements starting with SELECT or WITH.
func looksReadOnly(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	return strings.HasPrefix(q, "select") || strings.HasPrefix(q, "with")
}

// readRows loads every row into memory. Fine for a personal CLI; users can
// add LIMIT for big tables.
func readRows(rows *sql.Rows) ([]string, [][]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	var data [][]any
	for rows.Next() {
		// Scan needs pointers; each *any receives whatever type SQLite returns.
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok { // BLOB/TEXT may arrive as bytes
				vals[i] = string(b)
			}
		}
		data = append(data, vals)
	}
	return cols, data, rows.Err()
}

func writeJSON(w io.Writer, cols []string, data [][]any) error {
	// Start from an empty slice so zero rows print [] rather than null.
	out := []map[string]any{}
	for _, row := range data {
		obj := make(map[string]any, len(cols))
		for i, c := range cols {
			obj[c] = row[i]
		}
		out = append(out, obj)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func writeTable(w io.Writer, cols []string, data [][]any) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(cols, "\t"))
	for _, row := range data {
		cells := make([]string, len(row))
		for i, v := range row {
			if v == nil {
				cells[i] = "NULL"
			} else {
				cells[i] = fmt.Sprint(v)
			}
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return tw.Flush()
}
