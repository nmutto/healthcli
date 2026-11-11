package cli

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newQueryCmd(opts *options) *cobra.Command {
	var limit int
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "query <sql>",
		Short: "Run a read-only SQL query",
		Long: `Run one read-only SQL query (SELECT, WITH or EXPLAIN) against the local database and print the rows.
The database is opened read-only; a second statement, PRAGMA and ATTACH are refused.
Rows are printed as they are read, so a big result does not fill the memory.
At most --limit rows are printed (default 1000); a note on stderr says when more exist.
Main table: samples(metric, start_ts, end_ts, value, unit, tz_offset, source).`,
		Example: `  healthcli query "SELECT count(*) FROM samples"
  healthcli query "SELECT * FROM samples LIMIT 5" --json
  healthcli query "SELECT * FROM samples" --limit 0`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 {
				return usageErrorf("--limit must be 0 or more, got %d", limit)
			}
			if timeout < 0 {
				return usageErrorf("--timeout must be 0 or more, got %s", timeout)
			}
			return runQuery(cmd, opts, args[0], limit, timeout)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", defaultQueryLimit, "most rows to print (0 = no limit)")
	cmd.Flags().DurationVar(&timeout, "timeout", defaultQueryTimeout, "stop the query after this long (0 = no timeout)")
	return cmd
}

const (
	defaultQueryLimit   = 1000
	defaultQueryTimeout = 30 * time.Second
	// tableBlock is how many table rows are aligned together. A tabwriter
	// keeps its rows in memory until it is flushed, so we flush every block.
	tableBlock = 1000
)

func runQuery(cmd *cobra.Command, opts *options, query string, limit int, timeout time.Duration) error {
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

	// The context ends the query on Ctrl-C and when the timeout runs out.
	ctx := cmdContext(cmd)
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return queryError(ctx, err, timeout)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return queryError(ctx, err, timeout)
	}
	var out rowWriter
	if opts.jsonOutput {
		out = newJSONRows(cmd.OutOrStdout(), cols)
	} else {
		out = newTableRows(cmd.OutOrStdout(), cols)
	}

	shown := 0
	more := false
	for rows.Next() {
		if limit > 0 && shown == limit {
			more = true // one row past the limit: there is more to see
			break
		}
		row, err := scanRow(rows, len(cols))
		if err != nil {
			return queryError(ctx, err, timeout)
		}
		if err := out.Write(row); err != nil {
			return err
		}
		shown++
	}
	if err := rows.Err(); err != nil {
		return queryError(ctx, err, timeout)
	}
	if err := out.Close(); err != nil {
		return err
	}
	if more {
		fmt.Fprintf(cmd.ErrOrStderr(), "%d rows shown, more exist; use --limit to see more (0 = all)\n", shown)
	}
	return nil
}

// queryError says so when the timeout (not the SQL) stopped the query.
func queryError(ctx context.Context, err error, timeout time.Duration) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("query stopped after %s; use --timeout to allow longer (0 = no timeout)", timeout)
	}
	return err
}

// checkReadOnlySQL accepts exactly one statement that starts with SELECT,
// WITH or EXPLAIN. Leading comments are fine and so is a trailing ";".
// ATTACH and PRAGMA can only come second or first, so both are refused too.
func checkReadOnlySQL(query string) error {
	words, semicolonAt := scanSQL(query)
	if len(words) == 0 {
		return fmt.Errorf("only SELECT, WITH or EXPLAIN queries are allowed")
	}
	first := words[0]
	if first == "explain" {
		// EXPLAIN [QUERY PLAN] <statement>: look at the statement itself.
		words = words[1:]
		if len(words) >= 2 && words[0] == "query" && words[1] == "plan" {
			words = words[2:]
		}
		if len(words) == 0 {
			return fmt.Errorf("only SELECT, WITH or EXPLAIN queries are allowed")
		}
		first = words[0]
	}
	if first != "select" && first != "with" {
		return fmt.Errorf("only SELECT, WITH or EXPLAIN queries are allowed")
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

// scanRow reads the current row. BLOBs become hex text.
func scanRow(rows *sql.Rows, n int) ([]any, error) {
	// Scan needs pointers; each *any receives whatever type SQLite returns.
	vals := make([]any, n)
	ptrs := make([]any, n)
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	for i, v := range vals {
		if b, ok := v.([]byte); ok { // a BLOB: show it as hex, not as broken text
			vals[i] = hex.EncodeToString(b)
		}
	}
	return vals, nil
}

// rowWriter prints rows one at a time. Close finishes the output.
type rowWriter interface {
	Write(row []any) error
	Close() error
}

// jsonRows prints an array of objects whose keys follow the SELECT column
// order. A map[string]any would sort keys alphabetically, so we write the
// brackets and commas ourselves and let encoding/json format each piece.
type jsonRows struct {
	w     io.Writer
	keys  []string
	count int
}

func newJSONRows(w io.Writer, cols []string) *jsonRows {
	return &jsonRows{w: w, keys: uniqueKeys(cols)}
}

func (j *jsonRows) Write(row []any) error {
	var sb strings.Builder
	if j.count == 0 {
		sb.WriteString("[\n")
	} else {
		sb.WriteString(",\n")
	}
	sb.WriteString("  {")
	for i, c := range j.keys {
		if i > 0 {
			sb.WriteString(", ")
		}
		key, err := json.Marshal(c)
		if err != nil {
			return err
		}
		val, err := json.Marshal(jsonValue(row[i]))
		if err != nil {
			return err
		}
		sb.Write(key)
		sb.WriteString(": ")
		sb.Write(val)
	}
	sb.WriteString("}")
	j.count++
	_, err := io.WriteString(j.w, sb.String())
	return err
}

func (j *jsonRows) Close() error {
	if j.count == 0 {
		_, err := io.WriteString(j.w, "[]\n")
		return err
	}
	_, err := io.WriteString(j.w, "\n]\n")
	return err
}

// jsonValue turns numbers JSON cannot hold (Inf and NaN) into null.
func jsonValue(v any) any {
	if f, ok := v.(float64); ok && (math.IsInf(f, 0) || math.IsNaN(f)) {
		return nil
	}
	return v
}

// uniqueKeys gives repeated column names a suffix (a, a_2, a_3) so the JSON
// objects never hold the same key twice.
func uniqueKeys(cols []string) []string {
	used := map[string]bool{}
	keys := make([]string, len(cols))
	for i, c := range cols {
		key := c
		for n := 2; used[key]; n++ {
			key = fmt.Sprintf("%s_%d", c, n)
		}
		used[key] = true
		keys[i] = key
	}
	return keys
}

// tableRows prints an aligned text table. Columns are aligned within each
// block of tableBlock rows, so memory stays small for a long result.
type tableRows struct {
	tw    *tabwriter.Writer
	count int
}

func newTableRows(w io.Writer, cols []string) *tableRows {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(cols, "\t"))
	return &tableRows{tw: tw}
}

func (t *tableRows) Write(row []any) error {
	cells := make([]string, len(row))
	for i, v := range row {
		if v == nil {
			cells[i] = "NULL"
		} else {
			cells[i] = fmt.Sprint(v)
		}
	}
	fmt.Fprintln(t.tw, strings.Join(cells, "\t"))
	t.count++
	if t.count%tableBlock == 0 {
		return t.tw.Flush()
	}
	return nil
}

func (t *tableRows) Close() error { return t.tw.Flush() }
