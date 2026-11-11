package stats

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
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
	// RecentDays is the look-back for the sleep night.
	RecentDays = 30
)

// Row states. Every metric row is in exactly one of them.
const (
	StateOK         = "ok"          // fresh value with a usable baseline
	StateNoBaseline = "no_baseline" // fresh value, fewer than MinBaselineDays days of history
	StateStale      = "stale"       // newest value is older than StaleDays
	StateNoData     = "no_data"     // this metric has never been recorded
	StateInBedOnly  = "in_bed_only" // sleep only: the latest night has in-bed time and no sleep
)

// labels are short table names that override the registry label where the
// registry one is too long for a narrow table. A metric missing here uses its
// registry label (metrics.Metric.Label).
var labels = map[string]string{
	"step_count":         "Steps",
	"active_energy":      "Active energy",
	"heart_rate":         "Avg heart rate",
	"resting_heart_rate": "Resting HR",
	"hrv_sdnn":           "HRV",
	"body_mass":          "Weight",
}

// The json struct tags on the report types in this package (Overview,
// MetricRow, SleepReport, WorkoutsReport, Heatmap and the types inside them)
// are a PUBLIC CONTRACT: they are what "overview --json", "sleep --json" and
// "workouts --json" print, and people script against them. Renaming a tag,
// changing its type or dropping a field breaks those scripts. Add fields
// rather than change them, and update the --help text of the command (its
// golden file shows the change) in the same commit.

// Overview is the whole report. The json tags are the documented --json shape.
type Overview struct {
	Today   string      `json:"today"`   // the local day the report is relative to
	Empty   bool        `json:"empty"`   // true when the database holds no data at all
	Metrics []MetricRow `json:"metrics"` // only metrics that have samples
	// Categories has one summary per category that has at least one metric,
	// in display order. Empty categories are left out.
	Categories []CategorySummary `json:"categories"`
	// NotTracked lists the registry metrics with no samples at all. The text
	// output prints just their count.
	NotTracked []string        `json:"not_tracked"`
	Sleep      OverviewSleep   `json:"sleep"`
	Workouts   WorkoutsSummary `json:"workouts"`
	// Heatmap is the activity calendar, left out when there is no activity
	// metric with data (or the caller asked for 0 weeks).
	Heatmap *Heatmap   `json:"heatmap,omitempty"`
	Data    DataHealth `json:"data"`

	// The rich reports behind the SLEEP and WORKOUTS sections. They are left
	// out (not null) when the database has no sleep or no workouts.
	SleepSummary   *SleepReport    `json:"sleep_summary,omitempty"`
	WorkoutsReport *WorkoutsReport `json:"workouts_report,omitempty"`
	// Heatmaps holds the metric calendars asked for with HeatMetrics, by
	// metric name; metrics with no data are left out.
	Heatmaps map[string]*Heatmap `json:"heatmaps,omitempty"`
}

// CategorySummary counts the metric rows of one category by outcome. Typical,
// Better, Worse and Changed add up to the rows with a baseline; Stale,
// NoBaseline and NoData cover the rest, so the six add up to Total.
type CategorySummary struct {
	Name       string `json:"name"`
	Total      int    `json:"total"`
	Better     int    `json:"better"`
	Worse      int    `json:"worse"`
	Changed    int    `json:"changed"`
	Typical    int    `json:"typical"`
	Stale      int    `json:"stale"`
	NoBaseline int    `json:"no_baseline"`
	NoData     int    `json:"no_data"`
}

// CategorySummaries counts the rows per category, in the order the categories
// first appear (BuildOverview sorts rows by category, so that is display order).
func CategorySummaries(rows []MetricRow) []CategorySummary {
	out := []CategorySummary{}
	idx := map[string]int{}
	for _, r := range rows {
		i, ok := idx[r.Category]
		if !ok {
			i = len(out)
			idx[r.Category] = i
			out = append(out, CategorySummary{Name: r.Category})
		}
		c := &out[i]
		c.Total++
		switch r.State {
		case StateOK:
			switch r.Verdict {
			case VerdictBetter:
				c.Better++
			case VerdictWorse:
				c.Worse++
			case VerdictChanged:
				c.Changed++
			default:
				c.Typical++
			}
		case StateStale:
			c.Stale++
		case StateNoBaseline:
			c.NoBaseline++
		default:
			c.NoData++
		}
	}
	return out
}

// OverviewOptions are the knobs of BuildOverview.
type OverviewOptions struct {
	HeatWeeks  int    // weeks of heatmap; 0 leaves the heatmap out
	HeatMetric string // metric for the heatmap; "" picks one automatically
	// HeatMetrics are further metric calendars to build (Overview.Heatmaps).
	HeatMetrics []string
}

// workoutsReportDays and workoutsReportWeeks are the windows of the workouts
// report inside the overview (the same defaults as "healthcli workouts").
const (
	workoutsReportDays  = 28
	workoutsReportWeeks = 8
)

// DefaultOverviewOptions is what "healthcli overview" does without flags.
func DefaultOverviewOptions() OverviewOptions { return OverviewOptions{HeatWeeks: 12} }

// autoHeatmapMetrics are tried in this order when no heatmap metric is given.
var autoHeatmapMetrics = []string{"step_count", "apple_exercise_time", "active_energy"}

// MetricRow is one metric, anchored on its OWN newest day (not on "today"),
// so a metric that was not recorded today still shows its last value.
type MetricRow struct {
	Metric    string   `json:"metric"`
	Label     string   `json:"label"`
	Short     string   `json:"-"` // Label for narrow tables
	Category  string   `json:"category"`
	Polarity  string   `json:"polarity"` // higher_better, lower_better, in_range or neutral
	Unit      string   `json:"unit"`
	State     string   `json:"state"`
	LatestDay string   `json:"latest_day,omitempty"` // empty for no_data
	AgeDays   int      `json:"age_days"`             // today minus LatestDay
	Value     *float64 `json:"value,omitempty"`      // daily value of LatestDay
	Source    string   `json:"source,omitempty"`

	// Baseline fields: DaysWithData is always set (for "needs 7 days"),
	// Mean and Stddev only when the baseline is usable.
	BaselineDays int      `json:"baseline_days_with_data"`
	Mean         *float64 `json:"baseline_mean,omitempty"`
	Stddev       *float64 `json:"baseline_stddev,omitempty"`
	DeltaPct     *float64 `json:"delta_pct,omitempty"` // omitted when the baseline mean is 0
	Z            *float64 `json:"z,omitempty"`
	Comparison   *string  `json:"comparison,omitempty"` // above, below or typical
	// Verdict is Comparison seen through Polarity: better, worse, changed
	// (beyond usual, but the metric has no good direction) or typical.
	// Empty unless State is ok. See Verdict for the caveat.
	Verdict string `json:"verdict,omitempty"`

	// Range is where the value sits against the metric's reference limit,
	// only for metrics that have one and a value that is not stale. It never
	// changes the verdict: a level against a population is not a change.
	Range *RangeCheck `json:"range,omitempty"`

	// Spark is one value per day for the last SparkDays days ending today
	// (null = no data that day).
	Spark []*float64 `json:"spark"`
}

// Positions of a value against a metric's reference limit.
const (
	RangeBelow  = "below"
	RangeWithin = "within"
	RangeAbove  = "above"
)

// RangeCheck is a metric's reference limit and where the latest value sits.
// A bound that is 0 (left out of the JSON) is no limit on that side.
type RangeCheck struct {
	Low      float64 `json:"low,omitempty"`
	High     float64 `json:"high,omitempty"`
	Source   string  `json:"source"`
	Basis    string  `json:"basis,omitempty"` // what the limit is, in a few words, for the note
	Position string  `json:"position"`        // below, within or above
}

// rangeCheck places v against the metric's reference limit; nil when the
// metric has none.
func rangeCheck(m metrics.Metric, v float64) *RangeCheck {
	if m.Range == nil {
		return nil
	}
	rc := &RangeCheck{Low: m.Range.Low, High: m.Range.High, Source: m.Range.Source, Basis: m.Range.Basis, Position: RangeWithin}
	switch {
	case m.Range.Low > 0 && v < m.Range.Low:
		rc.Position = RangeBelow
	case m.Range.High > 0 && v > m.Range.High:
		rc.Position = RangeAbove
	}
	return rc
}

// Differs reports whether this row's value is unusual for the person.
func (r MetricRow) Differs() bool {
	return r.State == StateOK && r.Comparison != nil && *r.Comparison != Typical
}

// OverviewSleep is the most recent night within RecentDays.
type OverviewSleep struct {
	State string `json:"state"` // ok, stale, in_bed_only (latest night has no asleep time) or no_data
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

// WorkoutsSummary covers the last workoutsReportDays days, the same window
// as the workouts report, so the headline and the table agree.
type WorkoutsSummary struct {
	Days            int            `json:"days"`
	Count           int            `json:"count"`
	DurationSeconds int64          `json:"duration_seconds"`
	EnergyKcal      float64        `json:"energy_kcal"`
	Latest          *LatestWorkout `json:"latest,omitempty"` // left out when there are none
}

// LatestWorkout is the newest workout in the window.
type LatestWorkout struct {
	Activity  string   `json:"activity"`
	Day       string   `json:"day"`
	AgeDays   int      `json:"age_days"`
	DistanceM *float64 `json:"distance_m,omitempty"`
}

// DataHealth says how much data there is and how complete it looks.
type DataHealth struct {
	Samples     int64            `json:"samples"`
	SleepNights int64            `json:"sleep_nights"` // distinct nights with asleep time (in-bed-only nights are not counted)
	FirstDay    string           `json:"first_day,omitempty"`
	LastDay     string           `json:"last_day,omitempty"`
	SourceCount map[string]int64 `json:"source_samples"`        // by source class: watch, iphone, manual, other
	LastImport  *ImportInfo      `json:"last_import,omitempty"` // left out if nothing was imported yet
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

// MinRelativeChange is the smallest relative change (percent) that counts as
// better / worse / changed when the metric has no MinDelta of its own.
const MinRelativeChange = 2.0

// futureSlack is how far past "now" a sample may start and still count: a
// device in another time zone can be a day ahead. Later samples are ignored.
const futureSlack = 24 * time.Hour

// meaningful says whether a change is big enough to matter, on top of being
// beyond one standard deviation: a tiny wobble in a very steady metric is
// still "typical".
func meaningful(m metrics.Metric, c Comparison) bool {
	if m.MinDelta > 0 {
		return math.Abs(c.Delta) >= m.MinDelta
	}
	if !c.HasPct { // baseline mean 0: no percentage, so any change counts
		return c.Delta != 0
	}
	return math.Abs(c.DeltaPct) >= MinRelativeChange
}

// isFinite reports whether every number is a real, finite float.
func isFinite(xs ...float64) bool {
	for _, x := range xs {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}

// BuildOverview reads everything the overview shows. now decides what
// "today" is (its own local calendar date), which keeps tests deterministic.
func BuildOverview(ctx context.Context, conn *sql.DB, now time.Time, opts OverviewOptions) (*Overview, error) {
	today := now.Format(DayLayout)
	o := &Overview{Today: today, Metrics: []MetricRow{}, Categories: []CategorySummary{}, NotTracked: []string{}}

	for _, name := range metrics.Names() {
		row, err := buildRow(ctx, conn, name, today, now.Add(futureSlack).Unix())
		if err != nil {
			return nil, err
		}
		// Show only what the user's data contains: a device that never records
		// (say) respiratory rate gets no empty row for it, only a count.
		if row.State == StateNoData {
			o.NotTracked = append(o.NotTracked, name)
			continue
		}
		o.Metrics = append(o.Metrics, row)
	}
	// Group by category; SliceStable keeps registry order inside a group.
	sort.SliceStable(o.Metrics, func(i, j int) bool {
		return categoryRank(o.Metrics[i].Category) < categoryRank(o.Metrics[j].Category)
	})

	o.Categories = CategorySummaries(o.Metrics)

	var err error
	if o.Sleep, err = buildSleep(ctx, conn, today); err != nil {
		return nil, err
	}
	if o.Workouts, err = buildWorkouts(ctx, conn, today); err != nil {
		return nil, err
	}
	if o.Data, err = buildDataHealth(ctx, conn, o.Metrics, now); err != nil {
		return nil, err
	}
	if o.Heatmap, err = buildOverviewHeatmap(ctx, conn, o, now, opts); err != nil {
		return nil, err
	}
	if o.Heatmaps, err = buildMetricHeatmaps(ctx, conn, o, now, opts); err != nil {
		return nil, err
	}
	if err := o.buildRichSections(ctx, conn, now, opts); err != nil {
		return nil, err
	}
	o.Empty = o.Data.Samples == 0 && o.Sleep.State == StateNoData && o.Workouts.Count == 0
	return o, nil
}

// buildRichSections fills SleepSummary and WorkoutsReport; a section with
// nothing to show stays nil.
func (o *Overview) buildRichSections(ctx context.Context, conn *sql.DB, now time.Time, opts OverviewOptions) error {
	sleep, err := BuildSleepReport(ctx, conn, now, SleepOptions{Weeks: min(opts.HeatWeeks, MaxHeatmapWeeks)})
	if err != nil {
		return err
	}
	if !sleep.Empty {
		o.SleepSummary = sleep
	}
	wk, err := BuildWorkoutsReportWithHeatmap(ctx, conn, o.Today, workoutsReportDays, workoutsReportWeeks, min(opts.HeatWeeks, MaxHeatmapWeeks))
	if err != nil {
		return err
	}
	if wk.Count > 0 {
		o.WorkoutsReport = wk
	}
	return nil
}

// buildMetricHeatmaps builds the calendars of opts.HeatMetrics, skipping
// metrics with no data. It returns nil when there are none.
func buildMetricHeatmaps(ctx context.Context, conn *sql.DB, o *Overview, now time.Time, opts OverviewOptions) (map[string]*Heatmap, error) {
	if opts.HeatWeeks <= 0 {
		return nil, nil
	}
	has := map[string]bool{}
	for _, r := range o.Metrics {
		has[r.Metric] = true
	}
	var out map[string]*Heatmap
	for _, name := range opts.HeatMetrics {
		if !has[name] || out[name] != nil {
			continue
		}
		h, err := BuildHeatmap(ctx, conn, name, min(opts.HeatWeeks, MaxHeatmapWeeks), now)
		if err != nil {
			return nil, err
		}
		if h.Empty() {
			continue
		}
		if out == nil {
			out = map[string]*Heatmap{}
		}
		out[name] = h
	}
	return out, nil
}

// categoryRank is the display order of the groups.
func categoryRank(c string) int {
	for i, x := range []metrics.Category{metrics.Vitals, metrics.Activity, metrics.Body, metrics.Mobility, metrics.Other} {
		if string(x) == c {
			return i
		}
	}
	return 99
}

// buildOverviewHeatmap picks the activity metric (the requested one, else the
// first of autoHeatmapMetrics that has data) and builds its grid. It returns
// nil, not an error, when there is nothing to draw.
func buildOverviewHeatmap(ctx context.Context, conn *sql.DB, o *Overview, now time.Time, opts OverviewOptions) (*Heatmap, error) {
	if opts.HeatWeeks <= 0 {
		return nil, nil
	}
	has := map[string]bool{}
	for _, r := range o.Metrics {
		has[r.Metric] = true
	}
	candidates := autoHeatmapMetrics
	if opts.HeatMetric != "" {
		m, err := metrics.Lookup(opts.HeatMetric)
		if err != nil {
			return nil, err
		}
		candidates = []string{m.Name}
	}
	for _, name := range candidates {
		if !has[name] {
			continue
		}
		h, err := BuildHeatmap(ctx, conn, name, min(opts.HeatWeeks, MaxHeatmapWeeks), now)
		if err != nil {
			return nil, err
		}
		if !h.Empty() {
			return h, nil
		}
	}
	return nil, nil
}

// RangeVerdict is the verdict of an InRange metric: the LEVEL against the
// metric's range, not the direction of the move. Inside the band before and
// after, a move is not judged ("changed": plain higher or lower). Otherwise
// what counts is the distance outside the band: nearer than the usual is
// better, farther is worse. comparison is the usual above/below/typical
// label, so a move too small to matter stays typical.
func RangeVerdict(r *metrics.Range, value, usual float64, comparison string) string {
	if comparison != Above && comparison != Below {
		return VerdictTypical
	}
	if r == nil {
		return VerdictChanged
	}
	now, before := outsideBy(r, value), outsideBy(r, usual)
	switch {
	case now == 0 && before == 0:
		return VerdictChanged
	case now < before:
		return VerdictBetter
	case now > before:
		return VerdictWorse
	}
	return VerdictChanged
}

// outsideBy is how far v is outside the range: 0 inside it (or when the
// side has no bound).
func outsideBy(r *metrics.Range, v float64) float64 {
	switch {
	case r.Low > 0 && v < r.Low:
		return r.Low - v
	case r.High > 0 && v > r.High:
		return v - r.High
	}
	return 0
}

// Verdicts: a metric's comparison seen through its polarity.
const (
	VerdictBetter  = "better"
	VerdictWorse   = "worse"
	VerdictChanged = "changed" // beyond usual, but the metric has no good direction
	VerdictTypical = "typical"
)

// Verdict turns a comparison (above, below or typical) into better / worse /
// changed / typical for a metric of polarity p.
//
// Polarity is a general guide for the general population, not medical advice:
// what is healthy for one person (an athlete's low resting heart rate,
// deliberate weight change) can differ, which is why neutral metrics are only
// ever "changed".
func Verdict(p metrics.Polarity, comparison string) string {
	if comparison != Above && comparison != Below {
		return VerdictTypical
	}
	switch {
	case p == metrics.HigherBetter && comparison == Above, p == metrics.LowerBetter && comparison == Below:
		return VerdictBetter
	case p == metrics.HigherBetter || p == metrics.LowerBetter:
		return VerdictWorse
	}
	return VerdictChanged
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

func buildRow(ctx context.Context, conn *sql.DB, name, today string, maxStart int64) (MetricRow, error) {
	m, err := metrics.Lookup(name)
	if err != nil {
		return MetricRow{}, err
	}
	row := newRow(m)

	latest, err := latestSampleUpTo(ctx, conn, m.Name, maxStart)
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
	row.Source = daily[0].Source
	if !isFinite(v) { // a sum that overflowed: no usable number to show or compare
		row.State = StateNoBaseline
		return row, nil
	}
	row.Value = &v

	if row.Spark, err = sparkValues(ctx, conn, m.Name, today, latest.Day); err != nil {
		return MetricRow{}, err
	}

	if row.AgeDays > StaleDays {
		row.State = StateStale
		// No comparison for a stale value, but the baseline day count is real.
		b, err := BaselineFor(ctx, conn, m.Name, latest.Day, DefaultBaselineDays)
		if err != nil && !errors.Is(err, ErrNotEnoughData) {
			return MetricRow{}, err
		}
		row.BaselineDays = b.N
		return row, nil
	}
	if err := fillComparison(ctx, conn, &row, m, latest.Day, v); err != nil {
		return MetricRow{}, err
	}
	return row, nil
}

// newRow is an empty row of metric m (state no_data), with its table label.
func newRow(m metrics.Metric) MetricRow {
	label := labels[m.Name]
	if label == "" {
		label = m.Label
	}
	short := m.Short
	if short == "" {
		short = label
	}
	return MetricRow{Metric: m.Name, Label: label, Short: short, Category: string(m.Category), Polarity: string(m.Polarity), Unit: m.Unit, State: StateNoData, Spark: []*float64{}}
}

// fillComparison sets the baseline fields, state, comparison and verdict of
// row: the daily value v of `day` set against the 28 days before it. The
// overview anchors it on each metric's newest day, "today" on the day shown.
func fillComparison(ctx context.Context, conn *sql.DB, row *MetricRow, m metrics.Metric, day string, v float64) error {
	row.Range = rangeCheck(m, v) // independent of the baseline
	b, err := BaselineFor(ctx, conn, m.Name, day, DefaultBaselineDays)
	row.BaselineDays = b.N
	if errors.Is(err, ErrNotEnoughData) {
		row.State = StateNoBaseline
		return nil
	}
	if err != nil {
		return err
	}
	if !isFinite(b.Mean, b.Stddev) { // the baseline overflowed: cannot compare
		row.State = StateNoBaseline
		return nil
	}
	c := Compare(v, b)
	lab := c.Label
	if lab != Typical && !meaningful(m, c) {
		lab = Typical // beyond one stddev, but too small a change to matter
	}
	row.State = StateOK
	row.Mean, row.Stddev = &b.Mean, &b.Stddev
	row.Z, row.Comparison = &c.Z, &lab
	if c.HasPct {
		row.DeltaPct = &c.DeltaPct
	}
	if m.Polarity == metrics.InRange {
		row.Verdict = RangeVerdict(m.Range, v, b.Mean, lab)
	} else {
		row.Verdict = Verdict(m.Polarity, lab)
	}
	return nil
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
		if v, ok := byDay[day]; ok && isFinite(v) {
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
	if n.Asleep == 0 {
		s.State = StateInBedOnly
	}
	if s.AgeDays > StaleDays {
		s.State = StateStale
	}
	return s, nil
}

func buildWorkouts(ctx context.Context, conn *sql.DB, today string) (WorkoutsSummary, error) {
	w := WorkoutsSummary{Days: workoutsReportDays}
	from, err := AddDays(today, -(workoutsReportDays - 1))
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

func buildDataHealth(ctx context.Context, conn *sql.DB, rows []MetricRow, now time.Time) (DataHealth, error) {
	d := DataHealth{SourceCount: map[string]int64{}, Gaps: []Gap{}}

	// Rows per source, grouped into classes (a user can have several iPhones).
	bySource, total, err := countBySource(ctx, conn)
	if err != nil {
		return d, err
	}
	models, err := sourceModels(ctx, conn)
	if err != nil {
		return d, err
	}
	for source, n := range bySource {
		d.SourceCount[SourceClass(source, models[source])] += n
	}
	d.Samples = total

	err = conn.QueryRowContext(ctx, `
		SELECT count(DISTINCT date(end_ts + tz_offset + ?, 'unixepoch')) FROM sleep_stages
		WHERE stage IN ('core', 'deep', 'rem', 'asleep_unspecified')`, nightShift).Scan(&d.SleepNights)
	if err != nil {
		return d, fmt.Errorf("stats: count sleep nights: %w", err)
	}

	d.FirstDay, d.LastDay, _, err = sampleSpan(ctx, conn, now.Add(futureSlack).Unix())
	if err != nil {
		return d, err
	}

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
