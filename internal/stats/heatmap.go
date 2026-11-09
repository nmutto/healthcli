package stats

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/nmutto/healthcli/internal/metrics"
)

// MaxHeatmapWeeks is the longest window BuildHeatmap accepts (one year).
const MaxHeatmapWeeks = 53

// HeatLevels is the number of shades: quintiles of the metric's own values.
const HeatLevels = 5

// HeatDay is one calendar cell.
type HeatDay struct {
	Day    string   `json:"day"`
	Value  *float64 `json:"value"` // null: no data that day (a gap) or a future day
	Level  int      `json:"level"` // 1..5 (5 = highest fifth); 0 = no level: gap or future
	Future bool     `json:"future,omitempty"`
}

// Heatmap is a calendar grid: Weeks[i][0] is a Monday, Weeks[i][6] a Sunday,
// oldest week first. The last week is the one that holds today.
type Heatmap struct {
	Metric string `json:"metric"`
	Unit   string `json:"unit"`
	Today  string `json:"today"`
	From   string `json:"from"` // Monday of the first week
	// Thresholds are the 4 upper bounds of levels 1..4: level 1 is
	// value <= Thresholds[0], level 2 is <= Thresholds[1], ... and level 5 is
	// everything above Thresholds[3]. Empty when there is no data.
	Thresholds   []float64 `json:"thresholds"`
	DaysWithData int       `json:"days_with_data"`
	// Flat is true when every day with data has the same value (nothing is
	// high or low). Zero everywhere gets the lowest shade, anything else the
	// middle one.
	Flat  bool        `json:"flat,omitempty"`
	Weeks [][]HeatDay `json:"weeks"`
}

// Empty reports whether no day in the window has data.
func (h *Heatmap) Empty() bool { return h.DaysWithData == 0 }

// BuildHeatmap returns the last `weeks` calendar weeks (Monday to Sunday,
// ending with the week of `now`) of daily values for metric. Values come
// from DailySeries, so local-day and source-priority rules are the same as
// everywhere else.
func BuildHeatmap(ctx context.Context, conn *sql.DB, metric string, weeks int, now time.Time) (*Heatmap, error) {
	if weeks < 1 || weeks > MaxHeatmapWeeks {
		return nil, fmt.Errorf("stats: weeks must be between 1 and %d, got %d", MaxHeatmapWeeks, weeks)
	}
	m, err := metrics.Lookup(metric)
	if err != nil {
		return nil, err
	}
	today := now.Format(DayLayout)
	t, _ := time.Parse(DayLayout, today)
	// Go counts Sunday as 0; shift so Monday is 0.
	sinceMonday := (int(t.Weekday()) + 6) % 7
	from, err := AddDays(today, -(sinceMonday + 7*(weeks-1)))
	if err != nil {
		return nil, err
	}
	series, err := DailySeries(ctx, conn, m.Name, from, today)
	if err != nil {
		return nil, err
	}
	byDay := make(map[string]float64, len(series))
	for _, dv := range series {
		if isFinite(dv.Value) { // an overflowed sum cannot be drawn or printed
			byDay[dv.Day] = dv.Value
		}
	}
	return layoutHeatmap(m.Name, m.Unit, today, from, weeks, byDay), nil
}

// layoutHeatmap is the pure part of BuildHeatmap: values in, grid out.
func layoutHeatmap(metric, unit, today, from string, weeks int, byDay map[string]float64) *Heatmap {
	h := &Heatmap{Metric: metric, Unit: unit, Today: today, From: from, Thresholds: []float64{}}
	var values []float64
	for _, v := range byDay {
		values = append(values, v)
	}
	h.Thresholds = QuintileThresholds(values)
	h.DaysWithData = len(values)
	flat := allEqual(values)
	h.Flat = flat

	for w := 0; w < weeks; w++ {
		week := make([]HeatDay, 7)
		for d := range week {
			day, _ := AddDays(from, w*7+d)
			cell := HeatDay{Day: day}
			if day > today {
				cell.Future = true
			} else if v, ok := byDay[day]; ok {
				cell.Value = &v
				cell.Level = HeatLevel(v, h.Thresholds)
				if flat {
					cell.Level = flatLevel
					if v == 0 {
						cell.Level = 1 // a wall of zeros is "none", not "medium"
					}
				}
			}
			week[d] = cell
		}
		h.Weeks = append(h.Weeks, week)
	}
	return h
}

// QuintileThresholds returns the 4 cut points that split values into fifths,
// or nil for no values.
//
// Method: sort, then take the value at "nearest rank" 20%, 40%, 60% and 80%
// (index ceil(p*n)-1). Every threshold is therefore a real value from the
// data, which makes the legend honest ("up to 6,100 steps").
func QuintileThresholds(values []float64) []float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	out := make([]float64, HeatLevels-1)
	for i := range out {
		p := float64(i+1) / HeatLevels
		out[i] = sorted[int(math.Ceil(p*float64(len(sorted))))-1]
	}
	return out
}

// HeatLevel maps a value to a level 1..5: one plus the number of thresholds
// the value is strictly above. Equal values always get the same level, so
// with many repeated values some levels can stay unused (the legend then
// shows repeated bounds).
func HeatLevel(v float64, thresholds []float64) int {
	if len(thresholds) != HeatLevels-1 {
		return 0
	}
	level := 1
	for _, t := range thresholds {
		if v > t {
			level++
		}
	}
	return level
}

// flatLevel is used when every day has the same value: nothing is high or
// low, so show the middle shade, not a wall of the lowest one.
const flatLevel = 3

func allEqual(values []float64) bool {
	if len(values) == 0 {
		return false
	}
	for _, v := range values {
		if v != values[0] {
			return false
		}
	}
	return true
}
