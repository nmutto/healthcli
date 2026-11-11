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
	Value  *float64 `json:"value,omitempty"` // left out: no data that day (a gap) or a future day
	Level  int      `json:"level"`           // 1..5 (5 = highest fifth); 0 = no level: gap, future, or a rest day (Value is 0)
	Future bool     `json:"future,omitempty"`
}

// Heatmap is a calendar grid: Weeks[i][0] is a Monday, Weeks[i][6] a Sunday,
// oldest week first. The last week is the one that holds today.
type Heatmap struct {
	Metric string `json:"metric"`
	Unit   string `json:"unit"`
	// Title, Format and ZeroIsRest come from the HeatSpec; the renderer uses
	// them instead of looking at the metric name.
	Title      string               `json:"-"`
	Hue        string               `json:"-"`
	Format     func(float64) string `json:"-"`
	ZeroIsRest bool                 `json:"-"`
	Today      string               `json:"today"`
	From       string               `json:"from"` // Monday of the first week
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
	today, from, err := heatmapWindow(now, weeks)
	if err != nil {
		return nil, err
	}
	series, err := DailySeries(ctx, conn, m.Name, from, today)
	if err != nil {
		return nil, err
	}
	byDay := make(map[string]float64, len(series))
	for _, dv := range series {
		byDay[dv.Day] = dv.Value
	}
	// No Format here: the renderer formats a registry metric by name.
	spec := HeatSpec{Title: m.Name, Unit: m.Unit, Hue: m.Hue}
	return HeatmapFromDays(spec, now, weeks, byDay)
}

// Colour families a heatmap can ask for. They are defined in metrics, next to
// the registry that assigns them; the names are repeated here for callers.
const (
	HueOrange = metrics.HueOrange // steps and other activity (the default)
	HueGreen  = metrics.HueGreen  // workouts
	HueBlue   = metrics.HueBlue   // sleep
	HueRed    = metrics.HueRed    // heart rate metrics
	HueCyan   = metrics.HueCyan   // respiratory rate
	HuePurple = metrics.HuePurple // wrist temperature
	HueViolet = metrics.HueViolet // body mass
)

// HueFor is the colour family of a metric's heatmaps and trend lines: the
// Apple Health category colour from the registry, "" (orange) for everything
// without one. "sleep" (the trend of hours asleep) is blue. Only canonical
// names count, not aliases.
func HueFor(metric string) string {
	if metric == SleepMetric {
		return HueBlue
	}
	m, err := metrics.Lookup(metric)
	if err != nil || m.Name != metric {
		return ""
	}
	return m.Hue
}

// HeatSpec says how to title, label and shade one heatmap. Any source of
// "a number per day" (steps, sleep, workouts) can use the same engine.
type HeatSpec struct {
	Title  string // "step_count", "sleep", "workouts"
	Unit   string
	Hue    string               // colour family: HueGreen, HueBlue; empty means orange
	Format func(float64) string // how a value is written in the legend
	// ZeroIsRest: a day without a value after the first real one is a rest
	// day (value 0, drawn dim), not missing data. Quintiles then use the
	// non-zero days only. Days before the first value stay blank, so a new
	// device does not look like weeks of rest.
	ZeroIsRest bool
}

// heatmapWindow returns today's date and the Monday of the first week.
func heatmapWindow(now time.Time, weeks int) (today, from string, err error) {
	today = now.Format(DayLayout)
	t, _ := time.Parse(DayLayout, today)
	// Go counts Sunday as 0; shift so Monday is 0.
	sinceMonday := (int(t.Weekday()) + 6) % 7
	from, err = AddDays(today, -(sinceMonday + 7*(weeks-1)))
	return today, from, err
}

// HeatmapFromDays lays out `weeks` calendar weeks ending with the week of
// now, from values keyed by local day ("2006-01-02"). Days outside the
// window are ignored; non-finite values (an overflowed sum) are dropped
// because they cannot be drawn or printed.
func HeatmapFromDays(spec HeatSpec, now time.Time, weeks int, byDay map[string]float64) (*Heatmap, error) {
	if weeks < 1 || weeks > MaxHeatmapWeeks {
		return nil, fmt.Errorf("stats: weeks must be between 1 and %d, got %d", MaxHeatmapWeeks, weeks)
	}
	today, from, err := heatmapWindow(now, weeks)
	if err != nil {
		return nil, err
	}
	days := make(map[string]float64, len(byDay))
	// firstEver is the first non-zero day, even before the window: with a
	// history, the blank days at the start of the window are rest days.
	firstEver := ""
	for day, v := range byDay {
		if !isFinite(v) || day > today {
			continue
		}
		if v > 0 && (firstEver == "" || day < firstEver) {
			firstEver = day
		}
		if day >= from {
			days[day] = v
		}
	}
	h := layoutHeatmap(spec.Title, spec.Unit, today, from, weeks, days, spec.ZeroIsRest, firstEver)
	h.Title, h.Hue, h.Format, h.ZeroIsRest = spec.Title, spec.Hue, spec.Format, spec.ZeroIsRest
	return h, nil
}

// layoutHeatmap is the pure part: values in, grid out. firstEver is the first
// day with a real value, possibly before the window ("" if unknown): only used
// with zeroIsRest, where it decides where the rest days start.
func layoutHeatmap(metric, unit, today, from string, weeks int, byDay map[string]float64, zeroIsRest bool, firstEver string) *Heatmap {
	h := &Heatmap{Metric: metric, Unit: unit, Today: today, From: from, Thresholds: []float64{}}
	var values []float64
	if zeroIsRest {
		// Every day from the first non-zero one to today has a value; the
		// ones nobody wrote are real zeros (rest). Rests do not count when
		// cutting the shades.
		first := firstEver
		for day, v := range byDay {
			if v > 0 && (first == "" || day < first) {
				first = day
			}
		}
		if first != "" && first < from {
			first = from // the window starts here; earlier days are not drawn
		}
		filled := map[string]float64{}
		for day := first; first != "" && day <= today; {
			filled[day] = byDay[day]
			next, err := AddDays(day, 1)
			if err != nil {
				break // cannot step past the last day the calendar can write
			}
			day = next
		}
		byDay = filled
		for _, v := range byDay {
			if v > 0 {
				values = append(values, v)
			}
		}
	} else {
		for _, v := range byDay {
			values = append(values, v)
		}
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
				switch {
				case zeroIsRest && v == 0:
					// rest day: a value, but no shade
				case flat:
					cell.Level = flatLevel
					if v == 0 {
						cell.Level = 1 // a wall of zeros is "none", not "medium"
					}
				default:
					cell.Level = HeatLevel(v, h.Thresholds)
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
// With 5 or more values: sort, then take the value at "nearest rank" 20%, 40%,
// 60% and 80% (index ceil(p*n)-1). Every threshold is a real value from the
// data, which makes the legend honest ("up to 6,100 steps").
//
// With fewer than 5 values nearest rank makes the last cut the maximum, so
// the top level could never be reached. There the cuts are interpolated
// between neighbouring values instead (the 80% point lies between the two
// highest), which keeps the highest value above the last cut.
func QuintileThresholds(values []float64) []float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	n := len(sorted)
	out := make([]float64, HeatLevels-1)
	for i := range out {
		p := float64(i+1) / HeatLevels
		if n >= HeatLevels {
			out[i] = sorted[int(math.Ceil(p*float64(n)))-1]
			continue
		}
		pos := p * float64(n-1) // fractional index into the sorted values
		lo := int(pos)
		hi := min(lo+1, n-1)
		out[i] = sorted[lo] + (pos-float64(lo))*(sorted[hi]-sorted[lo])
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
