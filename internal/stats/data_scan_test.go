package stats

import (
	"context"
	"database/sql"
	"math/rand"
	"testing"
)

// The scans in data_scan.go must give what the plain full-table SQL gives.
// This builds random samples (several metrics and sources, offsets from -12 h
// to +14 h, some past the cut-off) and compares both ways.
func TestDataScansMatchPlainSQL(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	metricNames := []string{"step_count", "heart_rate", "body_mass", "hrv_sdnn"}
	sources := []string{watch, iphone, "Bob's Scale", "manual"}
	offsets := []int{-12 * 3600, -5 * 3600, 0, 3600, 19800, 14 * 3600}
	const base, cutoff = 1_700_000_000, 1_700_000_000 + 40*86400
	for round := range 20 {
		conn := newDB(t)
		for range rng.Intn(60) + 1 {
			ts := int64(base + rng.Intn(60*86400))
			_, err := conn.Exec(`INSERT OR IGNORE INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
				VALUES (?, ?, ?, 1, '', ?, ?)`, metricNames[rng.Intn(len(metricNames))], ts, ts+60,
				offsets[rng.Intn(len(offsets))], sources[rng.Intn(len(sources))])
			if err != nil {
				t.Fatal(err)
			}
		}
		ctx := context.Background()

		got, total, err := countBySource(ctx, conn)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]int64{}
		var wantTotal int64
		rows, err := conn.Query(`SELECT source, count(*) FROM samples GROUP BY source`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			var n int64
			rows.Scan(&s, &n)
			want[s] = n
			wantTotal += n
		}
		rows.Close()
		if total != wantTotal || len(got) != len(want) {
			t.Fatalf("round %d: counts %v (%d), want %v (%d)", round, got, total, want, wantTotal)
		}
		for s, n := range want {
			if got[s] != n {
				t.Errorf("round %d: source %q has %d, want %d", round, s, got[s], n)
			}
		}

		first, last, found, err := sampleSpan(ctx, conn, cutoff)
		if err != nil {
			t.Fatal(err)
		}
		var wf, wl sql.NullString
		err = conn.QueryRow(`SELECT min(date(start_ts + tz_offset, 'unixepoch')), max(date(start_ts + tz_offset, 'unixepoch'))
			FROM samples WHERE start_ts <= ?`, cutoff).Scan(&wf, &wl)
		if err != nil {
			t.Fatal(err)
		}
		if found != wf.Valid || first != wf.String || last != wl.String {
			t.Errorf("round %d: span %q..%q found=%v, want %q..%q found=%v", round, first, last, found, wf.String, wl.String, wf.Valid)
		}
	}
}

func TestDataScansOnEmptyDatabase(t *testing.T) {
	conn := newDB(t)
	counts, total, err := countBySource(context.Background(), conn)
	if err != nil || total != 0 || len(counts) != 0 {
		t.Errorf("counts %v total %d err %v", counts, total, err)
	}
	if _, _, found, err := sampleSpan(context.Background(), conn, 2_000_000_000); found || err != nil {
		t.Errorf("found %v err %v on an empty database", found, err)
	}
}
