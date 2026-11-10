package cli

import (
	"encoding/json"
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

func newLogCmd(opts *options) *cobra.Command {
	var unit, at string

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

--at accepts RFC3339 ("2025-03-01T08:30:00Z") or local time ("2025-03-01 08:30"),
up to one day in the future.`,
		Example: `  healthcli log weight 72.5
  healthcli log weight 160 --unit lb
  healthcli log hr 58 --at "2025-03-01 07:45"
  healthcli log steps 8000 --json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLog(cmd, opts, args[0], args[1], unit, at)
		},
	}
	cmd.Flags().StringVar(&unit, "unit", "", "unit of the value (default: the metric's own unit, e.g. kg for weight)")
	cmd.Flags().StringVar(&at, "at", "", `when it was measured: RFC3339 or "YYYY-MM-DD HH:MM" local time (default: now)`)
	return cmd
}

func runLog(cmd *cobra.Command, opts *options, metricArg, valueArg, unit, at string) error {
	metric, err := metrics.Lookup(metricArg)
	if err != nil {
		return err
	}
	raw, ok := metrics.ParseNumber(valueArg)
	if !ok {
		return fmt.Errorf("value %q is not a number", valueArg)
	}
	value, err := metric.ToCanonical(raw, unit)
	if err != nil {
		return err
	}
	// Round so unit conversion noise (72.57477920000001) never reaches the DB.
	value = math.Round(value*10000) / 10000
	if math.IsInf(value, 0) { // a huge value can overflow during conversion
		return fmt.Errorf("value %q is too large", valueArg)
	}
	if value < 0 || (value == 0 && metric.Name != "step_count" && metric.Name != "active_energy") {
		return fmt.Errorf("value %q is not a plausible %s", valueArg, metric.Name)
	}
	// Checked before anything is read from the database, so an impossible
	// value is reported as such and not as a duplicate.
	if err := metric.CheckValue(value); err != nil {
		return fmt.Errorf("cannot log %q: %w", valueArg, err)
	}
	now := time.Now()
	when, err := parseTime(at, now)
	if err != nil {
		return err
	}
	if when.After(now.Add(24 * time.Hour)) {
		return fmt.Errorf("--at %q is more than a day in the future", at)
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

	// An instant has no duration, so end_ts equals start_ts. ON CONFLICT DO
	// NOTHING makes logging the same row twice harmless.
	res, err := conn.Exec(`INSERT INTO samples (metric, start_ts, end_ts, value, unit, tz_offset, source)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		metric.Name, ts, ts, value, metric.Unit, offset, manualSource)
	if err != nil {
		return fmt.Errorf("save sample: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	inserted := n > 0
	if !inserted {
		// Same metric, instant and source but possibly another value: the old
		// one stays, and saying "no change" about the new one would mislead.
		var stored float64
		if err := conn.QueryRow(`SELECT value FROM samples
			WHERE metric = ? AND start_ts = ? AND end_ts = ? AND source = ?`,
			metric.Name, ts, ts, manualSource).Scan(&stored); err == nil && math.Abs(stored-value) > 1e-9 {
			return fmt.Errorf("%s is already logged as %s %s at that time; nothing changed",
				metric.Name, strconv.FormatFloat(stored, 'g', 6, 64), metric.Unit)
		}
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
	desc := fmt.Sprintf("%s = %s %s at %s", metric.Name, strconv.FormatFloat(value, 'g', 6, 64), metric.Unit, when.Format("2006-01-02 15:04"))
	if inserted {
		fmt.Fprintln(out, "Logged", desc)
	} else {
		fmt.Fprintln(out, "Already logged (no change):", desc)
	}
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
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf(`cannot read time %q: use RFC3339 (2025-03-01T08:30:00Z) or "YYYY-MM-DD HH:MM"`, s)
}
