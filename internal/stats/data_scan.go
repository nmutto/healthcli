package stats

import (
	"context"
	"database/sql"
	"fmt"
)

// This file holds the two whole-table questions of the data-health block:
// how many samples are there per source, and which days do they span. The
// obvious SQL (GROUP BY source, min/max of a computed date) reads all rows
// and takes seconds on millions of samples. These versions use the
// (metric, start_ts) index and give the same answers in a fraction of the
// time. They only read; nothing is stored.

// countBySource returns the number of samples per source name and their total.
//
// count(*) is fast (SQLite counts the smallest index) and so is a count of
// one source. GROUP BY source is not: it hashes every row. So the sources
// are found one at a time, each counted with its own query, until the counts
// add up to the total. A database has a handful of sources, so that is a
// handful of cheap queries.
func countBySource(ctx context.Context, conn *sql.DB) (map[string]int64, int64, error) {
	var total int64
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM samples`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("stats: count samples: %w", err)
	}
	counts := map[string]int64{}
	counted := int64(0)
	for counted < total {
		// A source not counted yet (the first row in index order that is not
		// one of the known sources).
		query, args := `SELECT source FROM samples LIMIT 1`, []any{}
		if len(counts) > 0 {
			marks := "?"
			for range len(counts) - 1 {
				marks += ",?"
			}
			query = `SELECT source FROM samples WHERE source NOT IN (` + marks + `) LIMIT 1`
			for s := range counts {
				args = append(args, s)
			}
		}
		var source string
		err := conn.QueryRowContext(ctx, query, args...).Scan(&source)
		if err == sql.ErrNoRows {
			break // rows were added while counting; the total was already read
		}
		if err != nil {
			return nil, 0, fmt.Errorf("stats: find source: %w", err)
		}
		var n int64
		if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM samples WHERE source = ?`, source).Scan(&n); err != nil {
			return nil, 0, fmt.Errorf("stats: count samples of a source: %w", err)
		}
		counts[source] = n
		counted += n
	}
	return counts, total, nil
}

// sampleSpan returns the first and last local day of the samples that start
// at or before maxStart; found is false when there are none.
//
// min(date(start_ts + tz_offset)) would visit every row. Instead, for each
// metric the index gives its first and last start_ts at once. Local time is
// start_ts plus an offset between -12 h and +14 h, so only rows within 26 h
// of the overall first (or last) start_ts can hold the first (or last) local
// day, and those are read through the index too.
func sampleSpan(ctx context.Context, conn *sql.DB, maxStart int64) (first, last string, found bool, err error) {
	names, err := sampleMetrics(ctx, conn)
	if err != nil {
		return "", "", false, err
	}
	const spread = maxOffsetEast + maxOffsetWest // seconds

	// The earliest and latest start_ts of any metric.
	var lo, hi int64
	for _, name := range names {
		// Two separate min/max subqueries: SQLite only answers a lone min() or
		// max() with a single seek, not a min() and a max() in one query.
		var a, b sql.NullInt64 // NULL when the metric has no row up to maxStart
		err := conn.QueryRowContext(ctx, `
			SELECT (SELECT min(start_ts) FROM samples WHERE metric = ?1 AND start_ts <= ?2),
			       (SELECT max(start_ts) FROM samples WHERE metric = ?1 AND start_ts <= ?2)`,
			name, maxStart).Scan(&a, &b)
		if err != nil {
			return "", "", false, fmt.Errorf("stats: sample range: %w", err)
		}
		if !a.Valid {
			continue
		}
		if !found || a.Int64 < lo {
			lo = a.Int64
		}
		if !found || b.Int64 > hi {
			hi = b.Int64
		}
		found = true
	}
	if !found {
		return "", "", false, nil
	}

	// The days of the rows near the two ends.
	for _, name := range names {
		var a, b sql.NullString
		err := conn.QueryRowContext(ctx, `
			SELECT (SELECT min(date(start_ts + tz_offset, 'unixepoch')) FROM samples WHERE metric = ?1 AND start_ts <= ?2),
			       (SELECT max(date(start_ts + tz_offset, 'unixepoch')) FROM samples WHERE metric = ?1 AND start_ts >= ?3 AND start_ts <= ?4)`,
			name, lo+spread, hi-spread, maxStart).Scan(&a, &b)
		if err != nil {
			return "", "", false, fmt.Errorf("stats: sample range: %w", err)
		}
		if a.Valid && (first == "" || a.String < first) {
			first = a.String
		}
		if b.Valid && b.String > last {
			last = b.String
		}
	}
	return first, last, true, nil
}

// sampleMetrics lists the distinct metric names with a loose index scan: each
// step asks the index for the next name above the previous one, so it reads
// one index entry per metric, not one per sample.
func sampleMetrics(ctx context.Context, conn *sql.DB) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `
		WITH RECURSIVE names(name) AS (
			SELECT min(metric) FROM samples
			UNION ALL
			SELECT (SELECT min(metric) FROM samples WHERE metric > names.name) FROM names WHERE name IS NOT NULL
		)
		SELECT name FROM names WHERE name IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("stats: list metrics: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("stats: list metrics: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
