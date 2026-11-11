package cli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/nmutto/healthcli/internal/metrics"
	"github.com/spf13/cobra"
)

// manualSource marks rows typed in by the user, so they never collide with
// (or get mistaken for) rows imported from Apple Health.
const manualSource = "manual"

// localLayout is the zone-less --at format.
const localLayout = "2006-01-02 15:04"

// earliestLog is the oldest time `log --at` accepts: nothing in a health
// record is older, and a typo like 1925 would silently file a reading there.
var earliestLog = time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)

func newLogCmd(opts *options) *cobra.Command {
	var unit, at string
	var del bool

	cmd := &cobra.Command{
		Use:   "log <metric> <value>",
		Short: "Record a manual measurement",
		Long: `Log a single measurement with the current time (or --at).

Every metric healthcli knows works (the error for an unknown name lists them).
Aliases: hr (heart_rate), hrv (hrv_sdnn), rhr (resting_heart_rate),
rr (respiratory_rate), spo2 (oxygen_saturation), steps (step_count),
temp (apple_sleeping_wrist_temperature), vo2 (vo2_max), weight (body_mass),
bmi (body_mass_index).
Weight is stored in kg; use --unit lb to enter pounds.
Percentages are typed as 0-100 (97 for 97%); blood oxygen below 50 is refused.
Every metric has a plausible range, shared with the importer; a value outside
it is refused (for example a heart rate above 300).

--at accepts RFC3339 ("2025-03-01T08:30:00Z") or local time ("2025-03-01 08:30"),
from 1990-01-01 up to one day in the future. A local time that the clocks skip
or repeat (daylight saving) is refused: give an offset instead.

--delete removes the reading you logged for <metric> at --at. It never touches
imported rows.`,
		Example: `  healthcli log weight 72.5
  healthcli log weight 160 --unit lb
  healthcli log hr 58 --at "2025-03-01 07:45"
  healthcli log steps 8000 --json
  healthcli log --delete hr --at "2025-03-01 07:45"`,
		Args: func(cmd *cobra.Command, args []string) error {
			if del {
				return cobra.ExactArgs(1)(cmd, args)
			}
			return cobra.ExactArgs(2)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if del {
				return runLogDelete(cmd, opts, args[0], unit, at)
			}
			return runLog(cmd, opts, args[0], args[1], unit, at)
		},
	}
	cmd.Flags().StringVar(&unit, "unit", "", "unit of the value (default: the metric's own unit, e.g. kg for weight)")
	cmd.Flags().StringVar(&at, "at", "", `when it was measured: RFC3339 or "YYYY-MM-DD HH:MM" local time (default: now)`)
	cmd.Flags().BoolVar(&del, "delete", false, "delete the manual reading of <metric> at --at (imported rows are never deleted)")
	return cmd
}

func runLog(cmd *cobra.Command, opts *options, metricArg, valueArg, unit, at string) error {
	metric, value, err := parseReading(metricArg, valueArg, unit)
	if err != nil {
		return err
	}
	now := nowFunc()
	when, err := parseTime(at, now)
	if err != nil {
		return err
	}
	if err := checkLogTime(when, at, now); err != nil {
		return err
	}

	// tz_offset is seconds east of UTC of the time as typed: the offset written
	// in an RFC3339 --at, otherwise the local zone at that moment (so daylight
	// saving on the chosen date is respected).
	_, offset := when.Zone()
	ts := when.Unix()

	conn, err := openOrCreateDB(opts)
	if err != nil {
		return err
	}
	defer conn.Close()

	inserted, err := insertManual(conn, metric, value, ts, offset)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if opts.jsonOutput {
		enc := json.NewEncoder(out)
		return enc.Encode(map[string]any{
			"metric":    metric.Name,
			"value":     value,
			"unit":      metric.Unit,
			"start_ts":  ts,
			"end_ts":    ts,
			"tz_offset": offset,
			"source":    manualSource,
			"inserted":  inserted,
		})
	}
	desc := describeReading(metric, value, when)
	if inserted {
		fmt.Fprintln(out, "Logged", desc)
	} else {
		fmt.Fprintln(out, "Already logged (no change):", desc)
	}
	return nil
}

// parseReading turns the typed metric, number and unit into a metric and a
// value in its canonical unit, checked against the registry's plausibility
// rule (the same one the importer uses).
func parseReading(metricArg, valueArg, unit string) (metrics.Metric, float64, error) {
	metric, err := metrics.Lookup(metricArg)
	if err != nil {
		return metric, 0, err
	}
	raw, ok := metrics.ParseNumber(valueArg)
	if !ok {
		return metric, 0, fmt.Errorf("value %q is not a number", valueArg)
	}
	value, err := metric.ToCanonical(raw, unit)
	if err != nil {
		return metric, 0, err
	}
	// Round so unit conversion noise (72.57477920000001) never reaches the DB.
	value = math.Round(value*10000) / 10000
	if math.IsInf(value, 0) { // a huge value can overflow during conversion
		return metric, 0, fmt.Errorf("value %q is too large", valueArg)
	}
	// Checked before anything is read from the database, so an impossible
	// value is reported as such and not as a duplicate.
	if err := metric.Plausible(value); err != nil {
		return metric, 0, fmt.Errorf("cannot log %q: %w", valueArg, err)
	}
	return metric, value, nil
}

// checkLogTime refuses a time that cannot be a real measurement.
func checkLogTime(when time.Time, at string, now time.Time) error {
	if when.Before(earliestLog) {
		return usageErrorf("--at %q is before %s", at, earliestLog.Format("2006-01-02"))
	}
	if when.After(now.Add(24 * time.Hour)) {
		return usageErrorf("--at %q is more than a day in the future", at)
	}
	return nil
}

// insertManual stores one manual reading and reports whether a row was added.
// An instant has no duration, so end_ts equals start_ts. ON CONFLICT DO
// NOTHING makes logging the same row twice harmless; the one case that is not
// harmless, the same instant with another value, is an error.
func insertManual(conn *sql.DB, metric metrics.Metric, value float64, ts int64, offset int) (bool, error) {
	res, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		metric.Name, ts, ts, value, metric.Unit, offset, manualSource)
	if err != nil {
		return false, fmt.Errorf("save sample: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	// Same metric, instant and source but possibly another value: the old
	// one stays, and saying "no change" about the new one would mislead.
	var stored float64
	if err := conn.QueryRow(`SELECT value FROM samples
		WHERE metric = ? AND start_ts = ? AND end_ts = ? AND source = ?`,
		metric.Name, ts, ts, manualSource).Scan(&stored); err == nil && math.Abs(stored-value) > 1e-9 {
		return false, fmt.Errorf("%s is already logged as %s %s at that time; nothing changed",
			metric.Name, strconv.FormatFloat(stored, 'g', 6, 64), metric.Unit)
	}
	return false, nil
}

// describeReading is the one-line text for a logged or deleted reading.
func describeReading(metric metrics.Metric, value float64, when time.Time) string {
	return fmt.Sprintf("%s = %s %s at %s", metric.Name, strconv.FormatFloat(value, 'g', 6, 64), metric.Unit, when.Format("2006-01-02 15:04"))
}

// runLogDelete removes the reading typed by hand for metric at the given
// instant. It only ever touches rows whose source is "manual": imported Apple
// rows are never deleted by `log`.
func runLogDelete(cmd *cobra.Command, opts *options, metricArg, unit, at string) error {
	if at == "" {
		return usageErrorf("--delete needs --at, the time of the reading to delete")
	}
	if unit != "" {
		return usageErrorf("--unit cannot be used with --delete")
	}
	metric, err := metrics.Lookup(metricArg)
	if err != nil {
		return err
	}
	when, err := parseTime(at, nowFunc())
	if err != nil {
		return err
	}
	ts := when.Unix()
	_, offset := when.Zone()

	conn, err := openExistingWritableDB(opts)
	if err != nil {
		return err
	}
	defer conn.Close()

	var value float64
	err = conn.QueryRow(`SELECT value FROM samples
		WHERE metric = ? AND start_ts = ? AND end_ts = ? AND source = ?`,
		metric.Name, ts, ts, manualSource).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("no manual %s reading at %s; only readings typed with `healthcli log` can be deleted",
			metric.Name, when.Format("2006-01-02 15:04"))
	}
	if err != nil {
		return err
	}
	if _, err := conn.Exec(`DELETE FROM samples
		WHERE metric = ? AND start_ts = ? AND end_ts = ? AND source = ?`,
		metric.Name, ts, ts, manualSource); err != nil {
		return fmt.Errorf("delete sample: %w", err)
	}

	out := cmd.OutOrStdout()
	if opts.jsonOutput {
		return json.NewEncoder(out).Encode(map[string]any{
			"metric":    metric.Name,
			"value":     value,
			"unit":      metric.Unit,
			"start_ts":  ts,
			"end_ts":    ts,
			"tz_offset": offset,
			"source":    manualSource,
			"deleted":   true,
		})
	}
	fmt.Fprintln(out, "Deleted", describeReading(metric, value, when))
	return nil
}

// parseTime turns the --at text into a time. Empty means now.
// Zone-less input is read as local time because that is what a person
// typing "07:45" means.
func parseTime(s string, now time.Time) (time.Time, error) {
	if s == "" {
		// Whole seconds only: samples store Unix seconds anyway.
		return now.Truncate(time.Second), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation(localLayout, s, time.Local); err == nil {
		return t, checkLocalTime(s, t)
	}
	return time.Time{}, fmt.Errorf(`cannot read time %q: use RFC3339 (2025-03-01T08:30:00Z) or "YYYY-MM-DD HH:MM"`, s)
}

// checkLocalTime catches the two odd local times around a daylight saving
// change. In the spring gap (02:30 never happens) Go quietly moves the time, so
// the clock on the result no longer reads what was typed. In the autumn
// repeat (01:30 happens twice) Go picks one of them without saying which. Both
// are refused: guessing could file the reading an hour off, and an explicit
// RFC3339 offset says exactly what is meant.
func checkLocalTime(s string, t time.Time) error {
	if t.Format(localLayout) != s {
		return fmt.Errorf("%q does not exist in your time zone (the clocks skip it); give the time with an offset, like 2025-03-09T03:30:00-04:00", s)
	}
	_, off := t.Zone()
	for _, day := range []time.Duration{-24 * time.Hour, 24 * time.Hour} {
		_, otherOff := t.Add(day).Zone()
		if otherOff == off {
			continue
		}
		// The same clock reading under the other offset is a different instant.
		other := t.Add(time.Duration(off-otherOff) * time.Second)
		if _, o := other.Zone(); o == otherOff && other.Format(localLayout) == s {
			return fmt.Errorf("%q happens twice in your time zone (the clocks go back); give the time with an offset, like 2025-11-02T01:30:00-04:00", s)
		}
	}
	return nil
}
