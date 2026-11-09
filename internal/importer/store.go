package importer

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"time"
)

// batchSize is how many rows go into one transaction. One commit per row
// would be very slow (each commit syncs to disk); one giant transaction would
// hold everything in the journal. A few thousand is the usual sweet spot.
const batchSize = 5000

// INSERT ... ON CONFLICT DO NOTHING relies on the UNIQUE constraints in the
// schema. Only rows identical in (metric/stage/activity, start, end, source)
// are treated as duplicates, so iPhone and Watch rows are both kept.
const (
	insertSample = `INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`
	insertSleep = `INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`
	insertWorkout = `INSERT INTO workouts (activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`
)

// writer collects rows into batches and writes them through prepared statements.
type writer struct {
	conn    *sql.DB
	sum     *Summary
	tx      *sql.Tx
	sample  *sql.Stmt
	sleep   *sql.Stmt
	workout *sql.Stmt
	pending int // rows in the current transaction
}

func newWriter(conn *sql.DB, sum *Summary) (*writer, error) {
	w := &writer{conn: conn, sum: sum}
	if err := w.begin(); err != nil {
		return nil, err
	}
	return w, nil
}

// begin opens a transaction and prepares the three statements inside it.
func (w *writer) begin() error {
	tx, err := w.conn.Begin()
	if err != nil {
		return err
	}
	w.tx = tx
	w.pending = 0
	for _, p := range []struct {
		dst **sql.Stmt
		sql string
	}{{&w.sample, insertSample}, {&w.sleep, insertSleep}, {&w.workout, insertWorkout}} {
		if *p.dst, err = tx.Prepare(p.sql); err != nil {
			tx.Rollback()
			return err
		}
	}
	return nil
}

func (w *writer) addSample(r sampleData) error {
	return w.exec(w.sample, r.metric, r.start, r.end, r.value, r.unit, r.tz, r.source)
}

func (w *writer) addSleep(r sleepData) error {
	return w.exec(w.sleep, r.stage, r.start, r.end, r.tz, r.source)
}

func (w *writer) addWorkout(r workoutData) error {
	// A nil *float64 is stored as NULL, so convert it explicitly.
	var energy, distance any
	if r.energy != nil {
		energy = *r.energy
	}
	if r.distance != nil {
		distance = *r.distance
	}
	return w.exec(w.workout, r.activity, r.start, r.end, r.duration, energy, distance, r.tz, r.source)
}

// exec runs one insert and counts it as new or duplicate. When the batch is
// full it commits and starts the next one.
func (w *writer) exec(st *sql.Stmt, args ...any) error {
	res, err := st.Exec(args...)
	if err != nil {
		return fmt.Errorf("writing to database: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		w.sum.Inserted++
	} else {
		w.sum.Duplicates++
	}
	w.pending++
	if w.pending >= batchSize {
		if err := w.tx.Commit(); err != nil {
			return err
		}
		return w.begin()
	}
	return nil
}

// finish records last_import_ts and a JSON summary of this import (read by
// "healthcli overview"), then commits the final batch.
func (w *writer) finish(started time.Time) error {
	if err := w.setMeta("last_import_ts", strconv.FormatInt(started.Unix(), 10)); err != nil {
		return err
	}
	summary, err := json.Marshal(map[string]any{
		"at":         started.Unix(),
		"read":       w.sum.Read,
		"inserted":   w.sum.Inserted,
		"duplicates": w.sum.Duplicates,
		"skipped":    w.sum.Skipped,
	})
	if err != nil {
		return err
	}
	if err := w.setMeta("last_import_summary", string(summary)); err != nil {
		return err
	}
	return w.tx.Commit()
}

// setMeta inserts or replaces one key of the meta table.
func (w *writer) setMeta(key, value string) error {
	_, err := w.tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (w *writer) rollback() { w.tx.Rollback() }

// openExport returns a reader for the XML inside path. For a zip it streams
// the export.xml entry straight out of the archive (no temp file).
func openExport(p string) (io.ReadCloser, error) {
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("file not found: %s", p)
		}
		return nil, fmt.Errorf("cannot open %s: %w", p, err)
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		f.Close()
		return nil, fmt.Errorf("%s is not a file", p)
	}

	// Zip files start with "PK"; sniffing beats trusting the extension.
	magic := make([]byte, 2)
	if _, err := f.ReadAt(magic, 0); err == nil && string(magic) == "PK" {
		zr, err := zip.NewReader(f, st.Size())
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("%s is not a valid zip file: %w", p, err)
		}
		for _, zf := range zr.File {
			// Real exports use apple_health_export/export.xml; there is also
			// export_cda.xml, which we do not want.
			if path.Base(zf.Name) == "export.xml" {
				rc, err := zf.Open()
				if err != nil {
					f.Close()
					return nil, err
				}
				return &multiCloser{Reader: rc, closers: []io.Closer{rc, f}}, nil
			}
		}
		f.Close()
		return nil, fmt.Errorf("%s contains no export.xml", p)
	}
	return f, nil
}

// multiCloser reads from Reader and closes everything it was given.
type multiCloser struct {
	io.Reader
	closers []io.Closer
}

func (m *multiCloser) Close() error {
	var first error
	for _, c := range m.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
