package stats

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/nmutto/healthcli/internal/metrics"
)

const (
	// DefaultBaselineDays is how far back a baseline looks when the caller
	// passes 0: four weeks covers every weekday four times, so a lazy Sunday
	// or a busy Monday does not skew it.
	DefaultBaselineDays = 28
	// MinBaselineDays is the fewest days WITH DATA a baseline needs. Below
	// that, one odd day moves the mean too much to be worth comparing to.
	MinBaselineDays = 7
	// TypicalZ is the z-score threshold for the above/below labels. Within
	// one standard deviation (roughly 2 out of 3 normal days) is "typical".
	TypicalZ = 1.0
)

// ErrNotEnoughData means a baseline had fewer than MinBaselineDays days with
// data. The returned Baseline still carries N, so the CLI can say
// "only 3 of 7 days needed".
var ErrNotEnoughData = errors.New("stats: not enough data for a baseline")

// Baseline describes a metric's usual daily value.
type Baseline struct {
	Metric string
	From   string  // first day looked at
	To     string  // last day looked at (the day before the target day)
	Mean   float64 // mean of the daily values
	Stddev float64 // sample standard deviation of the daily values
	N      int     // days with data in From..To
}

// BaselineFor computes the baseline of metric for day: the daily values of
// the `days` days BEFORE day (day itself is excluded, so today's value is
// compared with the past, not with itself). days <= 0 means
// DefaultBaselineDays.
//
// If fewer than MinBaselineDays of those days have data, it returns the
// partial Baseline (with N set) and ErrNotEnoughData.
func BaselineFor(ctx context.Context, conn *sql.DB, metric, day string, days int) (Baseline, error) {
	m, err := metrics.Lookup(metric)
	if err != nil {
		return Baseline{}, err
	}
	if days <= 0 {
		days = DefaultBaselineDays
	}
	from, err := AddDays(day, -days)
	if err != nil {
		return Baseline{}, err
	}
	to, err := AddDays(day, -1)
	if err != nil {
		return Baseline{}, err
	}
	series, err := DailySeries(ctx, conn, m.Name, from, to)
	if err != nil {
		return Baseline{}, err
	}

	values := make([]float64, 0, len(series))
	for _, dv := range series {
		values = append(values, dv.Value)
	}
	b := Baseline{Metric: m.Name, From: from, To: to, N: len(values)}
	if b.N < MinBaselineDays {
		return b, fmt.Errorf("%w: %s has %d days in %s..%s, need %d",
			ErrNotEnoughData, m.Name, b.N, from, to, MinBaselineDays)
	}
	b.Mean, b.Stddev = meanStddev(values)
	return b, nil
}

// meanStddev returns the mean and the sample standard deviation (dividing
// by n-1, the usual choice when the values are a sample of "normal days").
// It needs at least two values; BaselineFor guarantees seven.
func meanStddev(values []float64) (mean, stddev float64) {
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean = sum / float64(len(values))

	var squares float64
	for _, v := range values {
		squares += (v - mean) * (v - mean)
	}
	stddev = math.Sqrt(squares / float64(len(values)-1))

	// Seven identical weigh-ins of 79.4 should give a spread of exactly 0,
	// but float rounding can leave something like 1e-15. Snap that to 0 so
	// Compare treats it as "no spread" instead of dividing by a speck.
	if tiny(stddev, mean) {
		stddev = 0
	}
	return mean, stddev
}

// tiny reports whether x is just float rounding noise next to scale.
func tiny(x, scale float64) bool {
	return math.Abs(x) <= 1e-9*math.Max(1, math.Abs(scale))
}

// finite clamps an overflowed ratio to the largest float, keeping its sign,
// because encoding/json refuses to print Inf or NaN (a NaN becomes 0).
func finite(x float64) float64 {
	switch {
	case math.IsNaN(x):
		return 0
	case math.IsInf(x, 1):
		return math.MaxFloat64
	case math.IsInf(x, -1):
		return -math.MaxFloat64
	}
	return x
}

// Labels returned by Compare.
const (
	Above   = "above"
	Below   = "below"
	Typical = "typical"
)

// Comparison is a value set against its baseline.
type Comparison struct {
	Value    float64
	Mean     float64
	Delta    float64 // Value - Mean, in the metric's unit
	DeltaPct float64 // Delta as a percentage of Mean; 0 when Mean is 0 (see HasPct)
	HasPct   bool    // false when Mean is 0: a percentage of zero does not exist
	Z        float64 // Delta in standard deviations; 0 when Stddev is 0
	Label    string  // Above, Below or Typical
}

// Compare sets value against baseline b.
//
// Label is Above when Z >= TypicalZ, Below when Z <= -TypicalZ, otherwise
// Typical. If every baseline day had the same value (Stddev 0), there is no
// spread to measure against, so any difference at all counts as above/below.
func Compare(value float64, b Baseline) Comparison {
	c := Comparison{Value: value, Mean: b.Mean, Delta: finite(value - b.Mean)}
	if tiny(c.Delta, b.Mean) {
		c.Delta = 0 // same value, give or take float rounding
	}
	if b.Mean != 0 {
		c.HasPct = true
		c.DeltaPct = finite(c.Delta / math.Abs(b.Mean) * 100)
	}

	if b.Stddev > 0 {
		c.Z = finite(c.Delta / b.Stddev)
	}
	switch {
	case b.Stddev > 0 && c.Z >= TypicalZ, b.Stddev == 0 && c.Delta > 0:
		c.Label = Above
	case b.Stddev > 0 && c.Z <= -TypicalZ, b.Stddev == 0 && c.Delta < 0:
		c.Label = Below
	default:
		c.Label = Typical
	}
	return c
}
