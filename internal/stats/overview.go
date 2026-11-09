package stats

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nmutto/healthcli/internal/metrics"
)

// This file builds the data behind "healthcli overview". The struct it
// returns is also what --json prints, so the text and the JSON can never
// disagree. The CLI only turns it into text.

const (
	// SparkDays is how many days the little trend line covers.
	SparkDays = 28
	// StaleDays: a metric whose newest day is older than this is "stale".
	// Its value is still shown, but not compared with a baseline.
	StaleDays = 3
	// RecentDays is the look-back for the sleep night and the workout total.
	RecentDays = 30
)

// Row states. Every metric row is in exactly one of them.
const (
	StateOK         = "ok"          // fresh value with a usable baseline
	StateNoBaseline = "no_baseline" // fresh value, fewer than MinBaselineDays days of history
	StateStale      = "stale"       // newest value is older than StaleDays
	StateNoData     = "no_data"     // this metric has never been recorded
)

// labels are the short names shown in the table. A metric missing here falls
// back to its stored name, so adding a metric never breaks the overview.
var labels = map[string]string{
	"step_count":         "Steps",
	"active_energy":      "Active energy",
	"heart_rate":         "Avg heart rate",
	"resting_heart_rate": "Resting HR",
	"hrv_sdnn":           "HRV",
	"body_mass":          "Weight",
}

// Overview is the whole report. The json tags are the documented --json shape.
type Overview struct {
	Today    string          `json:"today"` // the local day the report is relative to
	Empty    bool            `json:"empty"` // true when the database holds no data at all
	Metrics  []MetricRow     `json:"metrics"`
	Sleep    OverviewSleep   `json:"sleep"`
	Workouts WorkoutsSummary `json:"workouts"`
	Data     DataHealth      `json:"data"`
}

// MetricRow is one metric, anchored on its OWN newest day (not on "today"),
// so a metric that was not recorded today still shows its last value.
type MetricRow struct {
	Metric    string   `json:"metric"`
	Label     string   `json:"label"`
	Unit      string   `json:"unit"`
	State     string   `json:"state"`
	LatestDay string   `json:"latest_day,omitempty"` // empty for no_data
	AgeDays   int      `json:"age_days"`             // today minus LatestDay
	Value     *float64 `json:"value"`                // daily value of LatestDay
	Source    string   `json:"source,omitempty"`

	// Baseline fields: DaysWithData is always set (for "needs 7 days"),
	// Mean and Stddev only when the baseline is usable.
	BaselineDays int      `json:"baseline_days_with_data"`
	Mean         *float64 `json:"baseline_mean"`
	Stddev       *float64 `json:"baseline_stddev"`
	DeltaPct     *float64 `json:"delta_pct"`
	Z            *float64 `json:"z"`
	Comparison   *string  `json:"comparison"` // above, below or typical

	// Spark is one value per day for the last SparkDays days ending today
	// (null = no data that day).
	Spark []*float64 `json:"spark"`
}

// Differs reports whether this row's value is unusual for the person.
func (r MetricRow) Differs() bool {
	return r.State == StateOK && r.Comparison != nil && *r.Comparison != Typical
}

// OverviewSleep is the most recent night within RecentDays.
type OverviewSleep struct {
	State string `json:"state"` // ok, stale or no_data
	Night string `json:"night,omitempty"`
	// AgeDays is today minus Night.
	AgeDays    int    `json:"age_days"`
	Source     string `json:"source,omitempty"`
	AsleepSecs int64  `json:"asleep_seconds"`
	CoreSecs   int64  `json:"core_seconds"`
	DeepSecs   int64  `json:"deep_seconds"`
	REMSecs    int64  `json:"rem_seconds"`
	UnspecSecs int64  `json:"unspecified_seconds"`
	AwakeSecs  int64  `json:"awake_seconds"`
	InBedSecs  int64  `json:"in_bed_seconds"`
}

// WorkoutsSummary covers the last RecentDays days.
type WorkoutsSummary struct {
	Days            int            `json:"days"`
	Count           int            `json:"count"`
	DurationSeconds int64          `json:"duration_seconds"`
	EnergyKcal      float64        `json:"energy_kcal"`
	Latest          *LatestWorkout `json:"latest"` // null when there are none
}

// LatestWorkout is the newest workout in the window.
type LatestWorkout struct {
	Activity  string   `json:"activity"`
	Day       string   `json:"day"`
	AgeDays   int      `json:"age_days"`
	DistanceM *float64 `json:"distance_m"`
}

// DataHealth says how much data there is and how complete it looks.
type DataHealth struct {
	Samples     int64            `json:"samples"`
	SleepNights int64            `json:"sleep_nights"` // distinct nights with sleep rows
	FirstDay    string           `json:"first_day,omitempty"`
	LastDay     string           `json:"last_day,omitempty"`
	SourceCount map[string]int64 `json:"source_samples"` // by source class: watch, iphone, manual, other
	LastImport  *ImportInfo      `json:"last_import"`    // null if nothing was imported yet
	Gaps        []Gap            `json:"gaps"`
}

// ImportInfo is the summary of the last import, kept in the meta table.
type ImportInfo struct {
	At         int64            `json:"at"` // Unix seconds
	Read       int64            `json:"read"`
	Inserted   int64            `json:"inserted"`
	Duplicates int64            `json:"duplicates"`
	Skipped    map[string]int64 `json:"skipped"`
}

// Gap counts the days without data inside a metric's own recent history.
type Gap struct {
	Metric  string `json:"metric"`
	Label   string `json:"label"`
	Missing int    `json:"missing_days"`
	Span    int    `json:"span_days"`
}

// BuildOverview reads everything the overview shows. now decides what
// "today" is (its own local calendar date), which keeps tests deterministic.
func BuildOverview(ctx context.Context, conn *sql.DB, now time.Time) (*Overview, error) {
	today := now.Format(DayLayout)
	o := &Overview{Today: today, Metrics: []MetricRow{}}

	for _, name := range metrics.Names() {
		row, err := buildRow(ctx, conn, name, today)
		if err != nil {
			return nil, err
		}
		o.Metrics = append(o.Metrics, row)
	}

	var err error
	if o.Sleep, err = buildSleep(ctx, conn, today); err != nil {
		return nil, err
	}
	if o.Workouts, err = buildWorkouts(ctx, conn, today); err != nil {
		return nil, err
	}
	if o.Data, err = buildDataHealth(ctx, conn, o.Metrics); err != nil {
		return nil, err
	}
	o.Empty = o.Data.Samples == 0 && o.Sleep.State == StateNoData && o.Workouts.Count == 0
	return o, nil
}

// daysBetween is the number of calendar days from a to b (negative if b is
// earlier). Both are "YYYY-MM-DD" strings.
func daysBetween(a, b string) (int, error) {
	ta, err := parseDay(a)
	if err != nil {
		return 0, err
	}
	tb, err := parseDay(b)
	if err != nil {
		return 0, err
	}
	// Both are midnight UTC, so the division is exact (no DST in UTC).
	return int(tb.Sub(ta) / (24 * time.Hour)), nil
}

// ageOf is how many days ago day was. A day in the future (clock skew, or a
// device in another timezone) counts as 0.
func ageOf(today, day string) (int, error) {
	n, err := daysBetween(day, today)
	return max(n, 0), err
}

func buildRow(ctx context.Context, conn *sql.DB, name, today string) (MetricRow, error) {
	m, err := metrics.Lookup(name)
	if err != nil {
		return MetricRow{}, err
	}
	label := labels[m.Name]
	if label == "" {
		label = m.Name
	}
	row := MetricRow{Metric: m.Name, Label: label, Unit: m.Unit, State: StateNoData, Spark: []*float64{}}

	latest, err := LatestSample(ctx, conn, m.Name)
	if errors.Is(err, ErrNoData) {
		return row, nil
	}
	if err != nil {
		return MetricRow{}, err
	}
	row.LatestDay = latest.Day
	if row.AgeDays, err = ageOf(today, latest.Day); err != nil {
		return MetricRow{}, err
	}

	// The daily value of the newest day (sum/avg/latest per the metric).
	daily, err := DailySeries(ctx, conn, m.Name, latest.Day, latest.Day)
	if err != nil {
		return MetricRow{}, err
	}
	if len(daily) == 0 {
		return row, nil // cannot happen: the sample is on that day
	}
	v := daily[0].Value
	row.Value, row.Source = &v, daily[0].Source

	if row.Spark, err = sparkValues(ctx, conn, m.Name, today, latest.Day); err != nil {
		return MetricRow{}, err
	}

	if row.AgeDays > StaleDays {
		row.State = StateStale
		return row, nil
	}
	b, err := BaselineFor(ctx, conn, m.Name, latest.Day, DefaultBaselineDays)
	row.BaselineDays = b.N
	if errors.Is(err, ErrNotEnoughData) {
		row.State = StateNoBaseline
		return row, nil
	}
	if err != nil {
		return MetricRow{}, err
	}
	c := Compare(v, b)
	lab := c.Label
	row.State = StateOK
	row.Mean, row.Stddev = &b.Mean, &b.Stddev
	row.DeltaPct, row.Z, row.Comparison = &c.DeltaPct, &c.Z, &lab
	return row, nil
}

// sparkValues returns SparkDays values ending on the later of today and the
// metric's newest day; nil marks a day without data.
func sparkValues(ctx context.Context, conn *sql.DB, metric, today, latestDay string) ([]*float64, error) {
	end := max(today, latestDay) // "YYYY-MM-DD" strings sort like dates
	start, err := AddDays(end, -(SparkDays - 1))
	if err != nil {
		return nil, err
	}
	series, err := DailySeries(ctx, conn, metric, start, end)
	if err != nil {
		return nil, err
	}
	byDay := make(map[string]float64, len(series))
	for _, dv := range series {
		byDay[dv.Day] = dv.Value
	}
	out := make([]*float64, SparkDays)
	for i := range out {
		day, err := AddDays(start, i)
		if err != nil {
			return nil, err
		}
		if v, ok := byDay[day]; ok {
			out[i] = &v
		}
	}
	return out, nil
}

func buildSleep(ctx context.Context, conn *sql.DB, today string) (OverviewSleep, error) {
	s := OverviewSleep{State: StateNoData}
	from, err := AddDays(today, -(RecentDays - 1))
	if err != nil {
		return s, err
	}
	nights, err := SleepNights(ctx, conn, from, today)
	if err != nil || len(nights) == 0 {
		return s, err
	}
	n := nights[len(nights)-1] // sorted oldest first
	s = OverviewSleep{
		State: StateOK, Night: n.Night, Source: n.Source,
		AsleepSecs: int64(n.Asleep / time.Second), CoreSecs: int64(n.Core / time.Second),
		DeepSecs: int64(n.Deep / time.Second), REMSecs: int64(n.REM / time.Second),
		UnspecSecs: int64(n.Unspecified / time.Second), AwakeSecs: int64(n.Awake / time.Second),
		InBedSecs: int64(n.InBed / time.Second),
	}
	if s.AgeDays, err = ageOf(today, n.Night); err != nil {
		return s, err
	}
	if s.AgeDays > StaleDays {
		s.State = StateStale
	}
	return s, nil
}

func buildWorkouts(ctx context.Context, conn *sql.DB, today string) (WorkoutsSummary, error) {
	w := WorkoutsSummary{Days: RecentDays}
	from, err := AddDays(today, -(RecentDays - 1))
	if err != nil {
		return w, err
	}
	list, err := Workouts(ctx, conn, from, today)
	if err != nil {
		return w, err
	}
	for _, x := range list {
		w.Count++
		w.DurationSeconds += int64(x.Duration / time.Second)
		if x.EnergyKcal != nil {
			w.EnergyKcal += *x.EnergyKcal
		}
	}
	if len(list) > 0 {
		last := list[len(list)-1] // oldest first, so the last one is the newest
		age, err := ageOf(today, last.Day)
		if err != nil {
			return w, err
		}
		w.Latest = &LatestWorkout{Activity: last.Activity, Day: last.Day, AgeDays: age, DistanceM: last.DistanceM}
	}
	return w, nil
}

func buildDataHealth(ctx context.Context, conn *sql.DB, rows []MetricRow) (DataHealth, error) {
	d := DataHealth{SourceCount: map[string]int64{}, Gaps: []Gap{}}

	// Rows per source, grouped into classes (a user can have several iPhones).
	res, err := conn.QueryContext(ctx, `SELECT source, count(*) FROM samples GROUP BY source`)
	if err != nil {
		return d, fmt.Errorf("stats: count samples: %w", err)
	}
	defer res.Close()
	for res.Next() {
		var source string
		var n int64
		if err := res.Scan(&source, &n); err != nil {
			return d, fmt.Errorf("stats: count samples: %w", err)
		}
		d.SourceCount[SourceClass(source)] += n
		d.Samples += n
	}
	if err := res.Err(); err != nil {
		return d, err
	}

	err = conn.QueryRowContext(ctx, `
		SELECT count(DISTINCT date(end_ts + tz_offset + ?, 'unixepoch')) FROM sleep_stages`, nightShift).Scan(&d.SleepNights)
	if err != nil {
		return d, fmt.Errorf("stats: count sleep nights: %w", err)
	}

	var first, last sql.NullString // NULL when the table is empty
	err = conn.QueryRowContext(ctx, `
		SELECT min(date(start_ts + tz_offset, 'unixepoch')), max(date(start_ts + tz_offset, 'unixepoch'))
		FROM samples`).Scan(&first, &last)
	if err != nil {
		return d, fmt.Errorf("stats: sample range: %w", err)
	}
	d.FirstDay, d.LastDay = first.String, last.String

	if d.LastImport, err = readLastImport(ctx, conn); err != nil {
		return d, err
	}

	// A gap is a day with no value between a metric's first day in the
	// spark window and the window's end. Days before the first value are
	// not "missing": the metric simply had not started yet.
	for _, r := range rows {
		firstIdx, lastIdx, present := -1, -1, 0
		for i, v := range r.Spark {
			if v == nil {
				continue
			}
			if firstIdx < 0 {
				firstIdx = i
			}
			lastIdx = i
			present++
		}
		if firstIdx < 0 {
			continue
		}
		span := lastIdx - firstIdx + 1
		if missing := span - present; missing > 0 {
			d.Gaps = append(d.Gaps, Gap{Metric: r.Metric, Label: r.Label, Missing: missing, Span: span})
		}
	}
	return d, nil
}

// Meta key the importer writes after each run (see importer.finish).
const (
	MetaLastImport = "last_import_summary" // JSON of ImportInfo
)

// readLastImport returns nil when the database was never imported into (or
// was imported by an older version that did not save the summary).
func readLastImport(ctx context.Context, conn *sql.DB) (*ImportInfo, error) {
	var raw string
	err := conn.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, MetaLastImport).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stats: read last import: %w", err)
	}
	var info ImportInfo
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		return nil, nil // a damaged value is not worth failing the command
	}
	if info.Skipped == nil {
		info.Skipped = map[string]int64{}
	}
	return &info, nil
}
