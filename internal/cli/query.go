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

// writeJSON prints an array of objects whose keys follow the SELECT column
// order. A map[string]any would sort keys alphabetically, so we write the
// brackets and commas ourselves and let encoding/json format each piece.
func writeJSON(w io.Writer, cols []string, data [][]any) error {
	if len(data) == 0 {
		_, err := io.WriteString(w, "[]\n")
		return err
	}
	var sb strings.Builder
	sb.WriteString("[\n")
	for r, row := range data {
		sb.WriteString("  {")
		for i, c := range cols {
			if i > 0 {
				sb.WriteString(", ")
			}
			key, err := json.Marshal(c)
			if err != nil {
				return err
			}
			val, err := json.Marshal(row[i])
			if err != nil {
				return err
			}
			sb.Write(key)
			sb.WriteString(": ")
			sb.Write(val)
		}
		sb.WriteString("}")
		if r < len(data)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("]\n")
	_, err := io.WriteString(w, sb.String())
	return err
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
