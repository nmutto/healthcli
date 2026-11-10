package stats

import (
	"math"
)

// TrendSummary is the min, max and mean of the days with data in a window.
type TrendSummary struct {
	DaysWithData int
	Min          float64
	MinDay       string
	Max          float64
	MaxDay       string
	Mean         float64
}

// WeekChange compares the mean of the last 7 days with the 7 days before.
// Available is false if either week has no data; the pointers are nil for a
// week without data (and Delta, DeltaPct unless both weeks have data).
type WeekChange struct {
	Available bool
	Last7Mean *float64
	Last7Days int
	PrevMean  *float64
	PrevDays  int
	Delta     *float64
	DeltaPct  *float64
}

// Summarize returns the summary of values, which are the daily values with
// data and days the matching local days. It needs at least one value.
func Summarize(values []float64, days []string) *TrendSummary {
	s := &TrendSummary{DaysWithData: len(values), Min: values[0], MinDay: days[0], Max: values[0], MaxDay: days[0]}
	var sum float64
	for i, v := range values {
		sum += v
		if v < s.Min {
			s.Min, s.MinDay = v, days[i]
		}
		if v > s.Max {
			s.Max, s.MaxDay = v, days[i]
		}
	}
	s.Mean = sum / float64(len(values))
	return s
}

// MeanOver averages the days from..to (inclusive) that have data.
func MeanOver(byDay map[string]DayValue, from, to string) (mean float64, n int) {
	var sum float64
	for d := from; d <= to; {
		if dv, ok := byDay[d]; ok {
			sum += dv.Value
			n++
		}
		next, err := AddDays(d, 1)
		if err != nil {
			break // end of the calendar; never loop on ""
		}
		d = next
	}
	if n == 0 {
		return 0, 0
	}
	return sum / float64(n), n
}

// WeekChangeFor compares the 7 days ending on end with the 7 before them.
func WeekChangeFor(byDay map[string]DayValue, end string) (WeekChange, error) {
	lastFrom, err := AddDays(end, -6)
	if err != nil {
		return WeekChange{}, err
	}
	prevTo, err := AddDays(end, -7)
	if err != nil {
		return WeekChange{}, err
	}
	prevFrom, err := AddDays(end, -13)
	if err != nil {
		return WeekChange{}, err
	}
	last, ln := MeanOver(byDay, lastFrom, end)
	prev, pn := MeanOver(byDay, prevFrom, prevTo)
	c := WeekChange{Last7Days: ln, PrevDays: pn}
	if ln > 0 {
		c.Last7Mean = &last
	}
	if pn > 0 {
		c.PrevMean = &prev
	}
	if ln > 0 && pn > 0 {
		c.Available = true
		d := last - prev
		if math.Abs(d) <= 1e-9*math.Max(1, math.Abs(prev)) {
			d = 0 // float noise, not a real change
		}
		c.Delta = &d
		pct := 0.0
		if prev != 0 {
			pct = d / math.Abs(prev) * 100
		}
		c.DeltaPct = &pct
	}
	return c, nil
}

// SleepBaseline is the baseline of the nights BEFORE end, from the nightly
// values in byDay. Sleep is not in the metrics registry, so BaselineFor
// cannot be used; this applies the same rule (mean of the 28 days before,
// at least MinBaselineDays with data). available is false below that, and n
// is still the number of days with data.
func SleepBaseline(byDay map[string]DayValue, end string) (mean float64, n int, available bool, err error) {
	from, err := AddDays(end, -DefaultBaselineDays)
	if err != nil {
		return 0, 0, false, err
	}
	to, err := AddDays(end, -1)
	if err != nil {
		return 0, 0, false, err
	}
	mean, n = MeanOver(byDay, from, to)
	return mean, n, n >= MinBaselineDays, nil
}
