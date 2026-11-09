package stats

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/nmutto/healthcli/internal/metrics"
)

// DayReport is everything "healthcli today" shows for one local day: the
// metrics that have a reading ON that day (each against the 28 days before
// it), the night of sleep that ended on it and its workouts.
type DayReport struct {
	Day string
	// Metrics are MetricRows like the overview's, but anchored on Day itself:
	// State is ok or no_baseline, AgeDays 0, Spark empty. Sorted by category.
	Metrics    []MetricRow
	Categories []CategorySummary
	// NoReading lists the tracked metrics (recorded on some other day) with no
	// reading on Day. Metrics the database has never seen are not listed.
	NoReading []string
	// Sleep is the report of the night that ended on Day; nil when none did.
	Sleep    *SleepReport
	Workouts []Workout
}

// Empty reports whether nothing at all was recorded on the day.
func (d *DayReport) Empty() bool {
	return len(d.Metrics) == 0 && d.Sleep == nil && len(d.Workouts) == 0
}

// BuildDay reads the day's data. It never fails for a day without data: that
// is an Empty report.
func BuildDay(ctx context.Context, conn *sql.DB, day string) (*DayReport, error) {
	t, err := parseDay(day)
	if err != nil {
		return nil, err
	}
	d := &DayReport{Day: day, Metrics: []MetricRow{}, Categories: []CategorySummary{}, NoReading: []string{}, Workouts: []Workout{}}

	for _, name := range metrics.Names() {
		m, err := metrics.Lookup(name)
		if err != nil {
			return nil, err
		}
		series, err := DailySeries(ctx, conn, name, day, day)
		if err != nil {
			return nil, err
		}
		if len(series) == 0 {
			// No reading today. Say so in the footer only if the metric is
			// tracked at all (has a sample on any day).
			if _, err := LatestSample(ctx, conn, name); err == nil {
				d.NoReading = append(d.NoReading, name)
			} else if !errors.Is(err, ErrNoData) {
				return nil, err
			}
			continue
		}
		v := series[0].Value
		row := newRow(m)
		row.LatestDay, row.Value, row.Source = day, &v, series[0].Source
		if !isFinite(v) {
			// An overflowed daily sum is still a reading (shown in the huge-number
			// format), but there is nothing sensible to compare it with.
			b, err := BaselineFor(ctx, conn, name, day, DefaultBaselineDays)
			if err != nil && !errors.Is(err, ErrNotEnoughData) {
				return nil, err
			}
			row.State, row.BaselineDays = StateNoBaseline, b.N
		} else if err := fillComparison(ctx, conn, &row, m, day, v); err != nil {
			return nil, err
		}
		d.Metrics = append(d.Metrics, row)
	}
	sort.SliceStable(d.Metrics, func(i, j int) bool {
		return categoryRank(d.Metrics[i].Category) < categoryRank(d.Metrics[j].Category)
	})
	d.Categories = CategorySummaries(d.Metrics)

	// Sleep: the report looks back from Day, so its newest night is Day's
	// night when one ended on it, and an older night otherwise.
	sleep, err := BuildSleepReport(ctx, conn, t, SleepOptions{UsualExcludesLast: true})
	if err != nil {
		return nil, err
	}
	if !sleep.Empty && sleep.Last.Night == day {
		d.Sleep = sleep
	}

	ws, err := Workouts(ctx, conn, day, day)
	if err != nil {
		return nil, err
	}
	for _, w := range ws {
		if w.Day == day {
			d.Workouts = append(d.Workouts, w)
		}
	}
	return d, nil
}
