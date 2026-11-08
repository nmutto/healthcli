// Package importer reads an Apple Health export (export.xml, or the zip that
// contains it) and stores it in the health database.
//
// The export can be several gigabytes, so nothing is ever loaded whole: we
// pull one XML token at a time, turn it into a row, and write rows to SQLite
// in batches. Memory use stays flat no matter how big the file is.
package importer

import (
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"
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
	Duplicates int64            `json:"duplicates"` // rows that already existed
	Skipped    map[string]int64 `json:"skipped"`    // reason -> count
	ElapsedSec float64          `json:"elapsed_seconds"`
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
}

const progressEvery = 100_000

// Import streams the export at path into conn. path may be export.xml or a
// zip containing it.
func Import(conn *sql.DB, path string, opts Options) (*Summary, error) {
	start := time.Now()
	src, err := openExport(path)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	sum := &Summary{Skipped: map[string]int64{}}
	w, err := newWriter(conn, sum)
	if err != nil {
		return nil, err
	}
	// If we return early, drop the half-finished batch. Rollback after a
	// successful Commit is a harmless no-op.
	defer w.rollback()

	if err := parse(src, sum, w, opts); err != nil {
		return nil, err
	}
	if err := w.finish(start); err != nil {
		return nil, err
	}
	sum.ElapsedSec = time.Since(start).Seconds()
	return sum, nil
}

// parse is the heart of the importer: walk the XML token by token and hand
// every <Record> and <Workout> to the matching converter.
func parse(r io.Reader, sum *Summary, w *writer, opts Options) error {
	dec := xml.NewDecoder(r)
	sawRoot := false

	for {
		tok, err := dec.Token() // next piece of XML; memory use is one token
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("not a valid Apple Health export (XML error: %w)", err)
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
				return errNotApple
			}
			sum.Read++
			if el.Name.Local == "Record" {
				err = handleRecord(el, sum, w)
			} else {
				err = handleWorkout(el, sum, w)
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
		return errNotApple
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
		return nil
	}
	return w.addSample(row)
}

func handleWorkout(el xml.StartElement, sum *Summary, w *writer) error {
	row, reason := workoutRow(attrs(el))
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

// number parses a float attribute. ok is false when it is empty or not numeric.
func number(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	// ParseFloat accepts "NaN" and "Inf"; neither is a measurement.
	return f, err == nil && finite(f)
}
