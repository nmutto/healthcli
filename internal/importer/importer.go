// Package importer reads an Apple Health export (export.xml, or the zip that
// contains it) and stores it in the health database.
//
// The export can be several gigabytes, so nothing is ever loaded whole: we
// pull one XML token at a time, turn it into a row, and write rows to SQLite
// in batches. Memory use stays flat no matter how big the file is.
package importer

import (
	"context"
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/nmutto/healthcli/internal/metrics"
)

// Skip reasons. Every record we cannot import is counted under one of these.
const (
	reasonUnknownType  = "unknown_type"
	reasonMissingValue = "missing_value"
	reasonBadValue     = "bad_value"
	reasonBadDate      = "bad_date"
	reasonBadUnit      = "bad_unit"
	reasonBadSleep     = "unknown_sleep_stage"
	reasonBadWorkout   = "bad_workout"
)

var errNotApple = errors.New("not an Apple Health export (no <HealthData> element found)")

// Summary is what an import did. The json tags are what `--json` prints.
type Summary struct {
	Read       int64            `json:"read"`       // Record and Workout elements seen
	Inserted   int64            `json:"inserted"`   // new rows written
	Duplicates int64            `json:"duplicates"` // same key and same value as a row already stored (or earlier in the file)
	Conflicts  int64            `json:"conflicts"`  // same key, different value: the incoming row was dropped
	Skipped    map[string]int64 `json:"skipped"`    // reason -> count
	// SkippedTypes counts unknown_type records by Apple identifier, so the
	// user can see what was left out. Capped at maxSkippedTypes distinct
	// identifiers; anything beyond that is only counted in Skipped.
	SkippedTypes map[string]int64 `json:"skipped_types,omitempty"`
	ElapsedSec   float64          `json:"elapsed_seconds"`
	// InvalidBytes counts bytes that were not valid UTF-8 (or were control
	// characters XML forbids) and were replaced by U+FFFD so the import could go on.
	InvalidBytes int64 `json:"invalid_bytes,omitempty"`
	// Interrupted is true when the context was cancelled (Ctrl-C): the rows
	// read so far are saved, the rest of the file was not looked at.
	Interrupted bool `json:"interrupted,omitempty"`
	// DryRun is true when nothing was written (see Options.DryRun).
	DryRun bool `json:"dry_run,omitempty"`
	// Backup is the safety copy made before writing, set by the caller.
	Backup string `json:"backup,omitempty"`
}

// SkippedTotal adds up all skip reasons.
func (s *Summary) SkippedTotal() int64 {
	var n int64
	for _, c := range s.Skipped {
		n += c
	}
	return n
}

// Options are the optional knobs of Import.
type Options struct {
	// Progress, if set, is called every progressEvery records with the
	// number of records read so far.
	Progress func(read int64)
	// DryRun parses and counts everything but writes nothing: the single
	// transaction is rolled back at the end.
	DryRun bool
}

const progressEvery = 100_000

// cancelCheckEvery is how many XML tokens pass between looks at the context.
// Cheap enough to ignore, frequent enough that Ctrl-C reacts within a blink.
const cancelCheckEvery = 4096

// fileError marks a problem with the file's content (as opposed to the
// database). Importing the same file again can never fix it.
type fileError struct{ err error }

func (e *fileError) Error() string { return e.err.Error() }
func (e *fileError) Unwrap() error { return e.err }

// maxSkippedTypes bounds the SkippedTypes map. A real export has well under a
// hundred identifiers; the cap only matters for a hostile or corrupt file.
const maxSkippedTypes = 500

// TopSkippedTypes returns up to n skipped Apple identifiers, most frequent
// first (ties broken alphabetically so output is stable).
func (s *Summary) TopSkippedTypes(n int) []SkippedType {
	out := make([]SkippedType, 0, len(s.SkippedTypes))
	for id, c := range s.SkippedTypes {
		out = append(out, SkippedType{Type: id, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Type < out[j].Type
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// SkippedType is one Apple identifier we did not import and how often it appeared.
type SkippedType struct {
	Type  string
	Count int64
}

// Import streams the export at path into conn. path may be export.xml or a
// zip containing it. It is ImportContext without a way to stop it.
func Import(conn *sql.DB, path string, opts Options) (*Summary, error) {
	return ImportContext(context.Background(), conn, path, opts)
}

// ImportContext is Import that can be stopped. When ctx is cancelled the rows
// read so far are committed and the summary says Interrupted; that is not an
// error.
func ImportContext(ctx context.Context, conn *sql.DB, path string, opts Options) (*Summary, error) {
	start := time.Now()
	src, err := openExport(path)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	sum := &Summary{Skipped: map[string]int64{}, SkippedTypes: map[string]int64{}, DryRun: opts.DryRun}
	w, err := newWriter(conn, sum, opts.DryRun)
	if err != nil {
		return nil, err
	}
	// If we return early, drop the half-finished batch. Rollback after a
	// successful Commit is a harmless no-op.
	defer w.rollback()

	clean := newCleanReader(src)
	err = parse(ctx, clean, sum, w, opts)
	sum.InvalidBytes = clean.replaced
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		// Stopped on request: keep what we have (the rows are valid, and
		// the summary counts them) and say so.
		sum.Interrupted = true
		if !opts.DryRun {
			if err := w.flush(); err != nil {
				return nil, err
			}
		}
		sum.ElapsedSec = time.Since(start).Seconds()
		return sum, nil
	}
	if err != nil {
		return nil, savedRowsNote(err, w)
	}
	if err := w.finish(start); err != nil {
		return nil, err
	}
	sum.ElapsedSec = time.Since(start).Seconds()
	return sum, nil
}

// savedRowsNote tells the user that earlier batches are already committed
// (the database is not as it was before the import), and only suggests
// running the import again when that can help.
func savedRowsNote(err error, w *writer) error {
	if w.committed == 0 {
		return err
	}
	var bad *fileError
	if errors.As(err, &bad) {
		return fmt.Errorf("%w; %d rows were saved before the error and are kept, but the file itself is damaged or incomplete, so importing it again will stop at the same place", err, w.committed)
	}
	return fmt.Errorf("%w; %d rows were saved before the error; once the cause is fixed, run the import again to complete it (re-importing is safe)", err, w.committed)
}

// parse is the heart of the importer: walk the XML token by token and hand
// every <Record> and <Workout> to the matching converter.
func parse(ctx context.Context, r io.Reader, sum *Summary, w *writer, opts Options) error {
	dec := xml.NewDecoder(r)
	sawRoot := false

	for tokens := 1; ; tokens++ {
		if tokens%cancelCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		tok, err := dec.Token() // next piece of XML; memory use is one token
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return &fileError{fmt.Errorf("not a valid Apple Health export (XML error: %w)", err)}
		}

		el, ok := tok.(xml.StartElement)
		if !ok {
			continue // text, comments, end tags: nothing to do
		}

		switch el.Name.Local {
		case "HealthData":
			sawRoot = true
		case "Record", "Workout":
			// Fail before writing anything if this is not an Apple export.
			if !sawRoot {
				return &fileError{errNotApple}
			}
			sum.Read++
			if el.Name.Local == "Record" {
				err = handleRecord(el, sum, w)
			} else {
				var stats []map[string]string
				if stats, err = workoutStatistics(dec); err == nil {
					err = handleWorkout(el, stats, sum, w)
				}
			}
			if err != nil {
				return err
			}
			// Only report right after a record: child elements must not
			// repeat the same milestone.
			if opts.Progress != nil && sum.Read%progressEvery == 0 {
				opts.Progress(sum.Read)
			}
		}
	}

	if !sawRoot {
		return &fileError{errNotApple}
	}
	return nil
}

// handleRecord converts one <Record> and queues it, or counts why not.
func handleRecord(el xml.StartElement, sum *Summary, w *writer) error {
	a := attrs(el)
	typ := a["type"]

	if typ == sleepType {
		row, reason := sleepRow(a)
		if reason != "" {
			sum.Skipped[reason]++
			return nil
		}
		return w.addSleep(row)
	}

	row, reason := sampleRow(a)
	if reason != "" {
		sum.Skipped[reason]++
		if reason == reasonUnknownType {
			sum.noteSkippedType(typ)
		}
		return nil
	}
	return w.addSample(row)
}

// noteSkippedType remembers an unknown Apple identifier (up to the cap).
func (s *Summary) noteSkippedType(typ string) {
	if _, seen := s.SkippedTypes[typ]; seen || len(s.SkippedTypes) < maxSkippedTypes {
		s.SkippedTypes[typ]++
	}
}

// workoutStatistics reads the rest of a <Workout> (the decoder is just past its
// start tag) and returns the attributes of its <WorkoutStatistics> children.
func workoutStatistics(dec *xml.Decoder) ([]map[string]string, error) {
	var stats []map[string]string
	for depth := 1; depth > 0; {
		tok, err := dec.Token()
		if err != nil { // io.EOF here means the file ends inside the workout
			return nil, &fileError{fmt.Errorf("not a valid Apple Health export (XML error: %w)", err)}
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "WorkoutStatistics" {
				stats = append(stats, attrs(t))
			}
		case xml.EndElement:
			depth--
		}
	}
	return stats, nil
}

func handleWorkout(el xml.StartElement, stats []map[string]string, sum *Summary, w *writer) error {
	row, reason := workoutRow(attrs(el), stats)
	if reason != "" {
		sum.Skipped[reason]++
		return nil
	}
	return w.addWorkout(row)
}

// attrs turns an element's attributes into a map for easy lookup.
func attrs(el xml.StartElement) map[string]string {
	m := make(map[string]string, len(el.Attr))
	for _, at := range el.Attr {
		m[at.Name.Local] = at.Value
	}
	return m
}

// number parses a float attribute. ok is false when it is empty or not a
// plain decimal number (see metrics.ParseNumber).
func number(s string) (float64, bool) {
	return metrics.ParseNumber(s)
}
