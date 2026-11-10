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
		Long: `Run one read-only SQL query (SELECT, WITH or EXPLAIN) against the local database and print the rows.
The database is opened read-only; a second statement, PRAGMA and ATTACH are refused.
Main table: samples(metric, start_ts, end_ts, value, unit, tz_offset, source).`,
		Example: `  healthcli query "SELECT count(*) FROM samples"
  healthcli query "SELECT * FROM samples LIMIT 5" --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runQuery(cmd, opts, args[0])
		},
	}
}

func runQuery(cmd *cobra.Command, opts *options, query string) error {
	// First line of defence: a friendly error for anything that is not a
	// single read.
	if err := checkReadOnlySQL(query); err != nil {
		return err
	}

	// Second line of defence: the connection itself is opened read-only, so
	// SQLite refuses every write, whatever the SQL says.
	conn, err := openReadOnlyDB(opts)
	if err != nil {
		return err
	}
	defer conn.Close()

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

// checkReadOnlySQL accepts exactly one statement that starts with SELECT,
// WITH or EXPLAIN. Leading comments are fine and so is a trailing ";".
// ATTACH and PRAGMA can only come second or first, so both are refused too.
func checkReadOnlySQL(query string) error {
	words, semicolonAt := scanSQL(query)
	if len(words) == 0 {
		return fmt.Errorf("only SELECT queries are allowed")
	}
	first := words[0]
	if first == "explain" {
		// EXPLAIN [QUERY PLAN] <statement>: look at the statement itself.
		words = words[1:]
		if len(words) >= 2 && words[0] == "query" && words[1] == "plan" {
			words = words[2:]
		}
		if len(words) == 0 {
			return fmt.Errorf("only SELECT queries are allowed")
		}
		first = words[0]
	}
	if first != "select" && first != "with" {
		return fmt.Errorf("only SELECT queries are allowed")
	}
	if semicolonAt >= 0 {
		return fmt.Errorf("only one statement is allowed per query")
	}
	return nil
}

// scanSQL walks the SQL text once. It skips comments and quoted text and
// returns the lower-case words of the first statement, plus the position of a
// ";" that is followed by more SQL (-1 when there is none). A single trailing
// ";" is not "more SQL".
func scanSQL(q string) (words []string, semicolonAt int) {
	semicolonAt = -1
	pendingSemicolon := -1
	i := 0
	for i < len(q) {
		c := q[i]
		switch {
		case c == '-' && strings.HasPrefix(q[i:], "--"):
			end := strings.IndexByte(q[i:], '\n')
			if end < 0 {
				return words, semicolonAt
			}
			i += end + 1
		case c == '/' && strings.HasPrefix(q[i:], "/*"):
			end := strings.Index(q[i+2:], "*/")
			if end < 0 {
				return words, semicolonAt
			}
			i += end + 4
		case c == '\'' || c == '"' || c == '`' || c == '[':
			closer := c
			if c == '[' {
				closer = ']'
			}
			i++
			for i < len(q) && q[i] != closer {
				i++
			}
			i++ // the closing quote (a doubled quote just reopens the text)
			if pendingSemicolon >= 0 {
				return words, pendingSemicolon
			}
			if len(words) < 4 {
				words = append(words, "?") // placeholder: quoted text is not a keyword
			}
		case c == ';':
			if pendingSemicolon < 0 {
				pendingSemicolon = i
			}
			i++
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		default:
			// Anything else after a ";" means a second statement.
			if pendingSemicolon >= 0 {
				return words, pendingSemicolon
			}
			start := i
			for i < len(q) && isWordByte(q[i]) {
				i++
			}
			if i == start {
				i++ // punctuation such as "(" or ","
				continue
			}
			if len(words) < 4 {
				words = append(words, strings.ToLower(q[start:i]))
			}
		}
	}
	return words, semicolonAt
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
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
