package importer

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"errors"
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
	insertSample = `INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source, device_model)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`
	insertSleep = `INSERT INTO sleep_stages (stage, start_ts, end_ts, tz_offset, source, device_model)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`
	insertWorkout = `INSERT INTO workouts (activity, start_ts, end_ts, duration_s, energy_kcal, distance_m, tz_offset, source, device_model)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`
)

// When an insert is dropped because the key exists, these ask whether the
// stored row holds a different value from the incoming one. They return 1 row
// if it does. "IS NOT" treats NULL as a value, so a missing energy equals a
// missing energy. Sleep rows have no value outside their key, so they have no
// such check.
const (
	sampleDiffers = `SELECT 1 FROM samples
		WHERE metric = ? AND start_ts = ? AND end_ts = ? AND source = ? AND value IS NOT ?`
	workoutDiffers = `SELECT 1 FROM workouts
		WHERE activity = ? AND start_ts = ? AND end_ts = ? AND source = ?
		AND (duration_s IS NOT ? OR energy_kcal IS NOT ? OR distance_m IS NOT ?)`
)

// Backfill: rows imported before migration 0003 have device_model = ”. When a
// re-import meets such a row and the incoming one knows the model, these fill
// it in. They never touch the key or the value, and a model that is already
// set is never replaced. They run only for rows the insert dropped, so they
// change nothing in the inserted/duplicate/conflict counts.
const (
	backfillSample = `UPDATE samples SET device_model = ?
		WHERE metric = ? AND start_ts = ? AND end_ts = ? AND source = ? AND device_model = ''`
	backfillSleep = `UPDATE sleep_stages SET device_model = ?
		WHERE stage = ? AND start_ts = ? AND end_ts = ? AND source = ? AND device_model = ''`
	backfillWorkout = `UPDATE workouts SET device_model = ?
		WHERE activity = ? AND start_ts = ? AND end_ts = ? AND source = ? AND device_model = ''`
)

// writer collects rows into batches and writes them through prepared statements.
type writer struct {
	conn    *sql.DB
	sum     *Summary
	tx      *sql.Tx
	sample  *sql.Stmt
	sleep   *sql.Stmt
	workout *sql.Stmt
	sampleDiff,
	workoutDiff *sql.Stmt
	sampleFill,
	sleepFill,
	workoutFill *sql.Stmt
	dryRun  bool // never commit: everything is rolled back at the end
	pending int  // rows in the current transaction

	// models remembers the device model of each source seen in this import
	// (non-empty ones only); finish saves it to meta so the overview can class
	// sources without scanning the samples table.
	models map[string]string

	committed int64 // rows in batches already committed; they stay saved if the import fails
}

func newWriter(conn *sql.DB, sum *Summary, dryRun bool) (*writer, error) {
	w := &writer{conn: conn, sum: sum, dryRun: dryRun, models: map[string]string{}}
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
	}{
		{&w.sample, insertSample}, {&w.sleep, insertSleep}, {&w.workout, insertWorkout},
		{&w.sampleDiff, sampleDiffers}, {&w.workoutDiff, workoutDiffers},
		{&w.sampleFill, backfillSample}, {&w.sleepFill, backfillSleep}, {&w.workoutFill, backfillWorkout},
	} {
		if *p.dst, err = tx.Prepare(p.sql); err != nil {
			tx.Rollback()
			return err
		}
	}
	return nil
}

func (w *writer) addSample(r sampleData) error {
	w.noteModel(r.source, r.deviceModel)
	differs := func() (bool, error) {
		return w.rowExists(w.sampleDiff, r.metric, r.start, r.end, r.source, r.value)
	}
	fill := w.backfill(w.sampleFill, r.deviceModel, r.metric, r.start, r.end, r.source)
	return w.exec(w.sample, differs, fill, r.metric, r.start, r.end, r.value, r.unit, r.tz, r.source, r.deviceModel)
}

func (w *writer) addSleep(r sleepData) error {
	w.noteModel(r.source, r.deviceModel)
	fill := w.backfill(w.sleepFill, r.deviceModel, r.stage, r.start, r.end, r.source)
	return w.exec(w.sleep, nil, fill, r.stage, r.start, r.end, r.tz, r.source, r.deviceModel)
}

func (w *writer) addWorkout(r workoutData) error {
	w.noteModel(r.source, r.deviceModel)
	// A nil *float64 is stored as NULL, so convert it explicitly.
	var energy, distance any
	if r.energy != nil {
		energy = *r.energy
	}
	if r.distance != nil {
		distance = *r.distance
	}
	differs := func() (bool, error) {
		return w.rowExists(w.workoutDiff, r.activity, r.start, r.end, r.source, r.duration, energy, distance)
	}
	fill := w.backfill(w.workoutFill, r.deviceModel, r.activity, r.start, r.end, r.source)
	return w.exec(w.workout, differs, fill, r.activity, r.start, r.end, r.duration, energy, distance, r.tz, r.source, r.deviceModel)
}

// noteModel remembers the first device model seen for a source.
func (w *writer) noteModel(source, model string) {
	if model != "" && w.models[source] == "" {
		w.models[source] = model
	}
}

// saveSourceModels merges this import's source -> device model pairs into the
// meta key "source_models" (a JSON object). A source keeps the model it
// already has there.
func (w *writer) saveSourceModels() error {
	if len(w.models) == 0 {
		return nil
	}
	stored := map[string]string{}
	var old string
	err := w.tx.QueryRow(`SELECT value FROM meta WHERE key = 'source_models'`).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_ = json.Unmarshal([]byte(old), &stored) // unreadable old value: start again
	for source, model := range w.models {
		if stored[source] == "" {
			stored[source] = model
		}
	}
	out, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	return w.setMeta("source_models", string(out))
}

// backfill returns the function exec calls for a row whose insert was dropped.
// It writes the incoming device model into the stored row if that row has none
// yet. A row that does not know its model has nothing to add, so it gets nil.
func (w *writer) backfill(st *sql.Stmt, model string, key ...any) func() error {
	if model == "" {
		return nil
	}
	return func() error {
		if _, err := st.Exec(append([]any{model}, key...)...); err != nil {
			return fmt.Errorf("filling in a device model: %w", err)
		}
		return nil
	}
}

// rowExists runs one of the "differs" queries and reports whether it found a row.
func (w *writer) rowExists(st *sql.Stmt, args ...any) (bool, error) {
	var one int
	err := st.QueryRow(args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking an existing row: %w", err)
	}
	return true, nil
}

// exec runs one insert and counts it as new, duplicate or conflict. differs
// (nil for sleep rows) says whether the row already stored under the same key
// holds another value; fill (nil when the row has no device model) writes the
// model into that stored row if it has none. When the batch is full it commits and starts the next.
func (w *writer) exec(st *sql.Stmt, differs func() (bool, error), fill func() error, args ...any) error {
	res, err := st.Exec(args...)
	if err != nil {
		return fmt.Errorf("writing to database: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("writing to database: %w", err)
	}
	if n == 0 && fill != nil {
		if err := fill(); err != nil {
			return err
		}
	}
	switch {
	case n > 0:
		w.sum.Inserted++
	case differs != nil:
		conflict, err := differs()
		if err != nil {
			return err
		}
		if conflict {
			w.sum.Conflicts++
		} else {
			w.sum.Duplicates++
		}
	default:
		w.sum.Duplicates++
	}
	w.pending++
	// A dry run keeps one transaction open to the end, so rows of the file
	// that repeat earlier rows of the same file are still seen as duplicates.
	if w.pending >= batchSize && !w.dryRun {
		return w.flush()
	}
	return nil
}

// flush commits the current batch and opens the next one.
func (w *writer) flush() error {
	if err := w.tx.Commit(); err != nil {
		return err
	}
	w.committed += int64(w.pending)
	return w.begin()
}

// finish records last_import_ts and a JSON summary of this import (read by
// "healthcli overview"), then commits the final batch.
func (w *writer) finish(started time.Time) error {
	if w.dryRun {
		return nil // the deferred rollback throws everything away
	}
	if err := w.setMeta("last_import_ts", strconv.FormatInt(started.Unix(), 10)); err != nil {
		return err
	}
	if err := w.saveSourceModels(); err != nil {
		return err
	}
	summary, err := json.Marshal(map[string]any{
		"at":       started.Unix(),
		"read":     w.sum.Read,
		"inserted": w.sum.Inserted,
		// "duplicates" keeps its old meaning here (every row dropped because
		// its key existed, so it includes the conflicts); "overview" reads it.
		// "conflicts" is the part of those whose value differed.
		"duplicates": w.sum.Duplicates + w.sum.Conflicts,
		"conflicts":  w.sum.Conflicts,
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

// Zip bomb guards. A real export is a few gigabytes of XML; these limits are
// far above that, and exist only so a hostile or corrupt archive cannot fill
// the disk or run for days. They are variables so a test can lower them.
var (
	maxEntryBytes   int64 = 64 << 30  // one entry, uncompressed
	maxArchiveBytes int64 = 128 << 30 // all entries together, as declared in the zip
)

// errTooBig is returned while reading an entry that grows past maxEntryBytes.
var errTooBig = errors.New("the zip entry is larger than the safety limit when unpacked (zip bomb?)")

// limitedReader is io.LimitReader, but running past the limit is an error
// instead of a quiet end of file (which would look like a truncated export).
type limitedReader struct {
	r    io.Reader
	left int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.left < 0 {
		return 0, errTooBig
	}
	if int64(len(p)) > l.left+1 {
		p = p[:l.left+1] // read one byte more than allowed, to notice
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	if l.left < 0 {
		return 0, errTooBig
	}
	return n, err
}

// gib formats a byte count for an error message.
func gib(n int64) string { return fmt.Sprintf("%d GiB", n>>30) }

// Check opens the export just far enough to know it can be read (file exists,
// is a file, a zip has an export.xml within the size limits) and closes it.
// Callers run it before they touch the database.
func Check(p string) error {
	src, err := openExport(p)
	if err != nil {
		return err
	}
	return src.Close()
}

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
		var declared int64
		for _, zf := range zr.File {
			declared += int64(zf.UncompressedSize64)
			if declared < 0 || declared > maxArchiveBytes {
				f.Close()
				return nil, fmt.Errorf("%s unpacks to more than %s, which is over the safety limit", p, gib(maxArchiveBytes))
			}
		}
		for _, zf := range zr.File {
			// Real exports use apple_health_export/export.xml; there is also
			// export_cda.xml, which we do not want.
			if path.Base(zf.Name) == "export.xml" {
				if zf.UncompressedSize64 > uint64(maxEntryBytes) {
					f.Close()
					return nil, fmt.Errorf("%s: export.xml unpacks to more than %s, which is over the safety limit", p, gib(maxEntryBytes))
				}
				rc, err := zf.Open()
				if err != nil {
					f.Close()
					return nil, err
				}
				// The sizes in a zip header can lie: count the real bytes too.
				return &multiCloser{Reader: &limitedReader{r: rc, left: maxEntryBytes}, closers: []io.Closer{rc, f}}, nil
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
