package stats

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

// This file turns the per-night rows of SleepNights into the numbers the
// SLEEP section shows: last night, 7/14-night averages, a "usual" over a
// longer window, bedtime regularity and sleep debt. Everything is a plain
// function of []SleepNight, so tests need no database.

// Defaults and limits of the sleep report.
const (
	DefaultSleepNights = 28 // nights in the "usual" window
	MinSleepNights     = 7  // fewest nights for a comparison with usual
	MaxSleepNights     = 90
	DefaultSleepTarget = 8 * time.Hour
	// MinHeatmapNights is how many nights with sleep a calendar needs; fewer
	// are shown as one small bar per night instead.
	MinHeatmapNights = 14
)

// SleepOptions says what BuildSleepReport computes. The zero value means
// "28 nights, 8 h target, hours-asleep heatmap, no heatmap" (Weeks 0 hides it).
type SleepOptions struct {
	Nights int           // nights in the usual window; 0 = 28
	Target time.Duration // nightly sleep goal; 0 = 8 h
	By     string        // heatmap value: asleep (default), bedtime, efficiency
	Weeks  int           // heatmap weeks; 0 = no heatmap
}

// SleepReport is the whole SLEEP section as data. Renderers and --json read
// the same struct.
type SleepReport struct {
	Today   string `json:"today"`
	Empty   bool   `json:"empty"` // no sleep rows at all in the window
	AgeDays int    `json:"age_days"`
	// Kind says what the last night has: "stages", "unspecified" (asleep but
	// no stages) or "in_bed" (in bed only, no sleep).
	Kind       string           `json:"kind,omitempty"`
	Night      *SleepNightView  `json:"night,omitempty"`
	Week       *SleepAgg        `json:"seven_nights,omitempty"`
	Fortnight  *SleepAgg        `json:"fourteen_nights,omitempty"`
	Usual      *SleepAgg        `json:"usual,omitempty"`
	Regularity *SleepRegularity `json:"regularity,omitempty"`
	Debt       *SleepDebt       `json:"debt,omitempty"`
	Heatmap    *Heatmap         `json:"heatmap,omitempty"`

	// For the renderer, not for JSON.
	Last        SleepNight   `json:"-"`
	Nights      []SleepNight `json:"-"` // every night read, oldest first
	UsualNights int          `json:"-"` // size of the usual window
}

// SleepAgg is the average of the nights of one window. A field is nil when
// no night of the window has that number (a device without stages has no
// deep sleep), so it is omitted rather than shown as zero.
type SleepAgg struct {
	Nights int `json:"nights"`
	// Durations are in seconds, Efficiency in percent. Bedtime, Wake and
	// Midpoint are minutes since 18:00 (so 00:30 is later than 23:30);
	// ClockText turns them back into "23:12".
	AsleepSecs  *float64 `json:"asleep_seconds,omitempty"`
	InBedSecs   *float64 `json:"in_bed_seconds,omitempty"`
	Efficiency  *float64 `json:"efficiency_pct,omitempty"`
	AwakeSecs   *float64 `json:"awake_seconds,omitempty"`
	Awakenings  *float64 `json:"awakenings,omitempty"`
	LatencySecs *float64 `json:"latency_seconds,omitempty"`
	DeepSecs    *float64 `json:"deep_seconds,omitempty"`
	REMSecs     *float64 `json:"rem_seconds,omitempty"`
	CoreSecs    *float64 `json:"core_seconds,omitempty"`
	BedtimeMin  *float64 `json:"bedtime_min_since_18h,omitempty"`
	WakeMin     *float64 `json:"wake_min_since_18h,omitempty"`
	MidpointMin *float64 `json:"midpoint_min_since_18h,omitempty"`
}

// SleepRegularity is how much bedtime and midpoint wander, as the standard
// deviation in minutes over Nights nights. Lower is more regular.
type SleepRegularity struct {
	Nights      int     `json:"nights"`
	BedtimeMin  float64 `json:"bedtime_stddev_min"`
	MidpointMin float64 `json:"midpoint_stddev_min"`
	// UsualBedtimeMin is the same bedtime spread over the whole usual
	// window; 0 when that window has fewer than 14 nights or is no longer
	// than the 14-night one.
	UsualBedtimeMin float64 `json:"usual_bedtime_stddev_min,omitempty"`
}

// SleepDebt compares sleep with the target. Positive seconds mean short of
// the target (owed), negative mean surplus.
type SleepDebt struct {
	TargetSecs int64 `json:"target_seconds"`
	LastSecs   int64 `json:"last_night_seconds"`
	Nights     int   `json:"nights"`         // nights counted in WindowSecs
	WindowSecs int64 `json:"window_seconds"` // the last 7 days, net
}

// SleepNightView is a night as JSON: seconds for durations, local clock
// times as "HH:MM".
type SleepNightView struct {
	Night        string    `json:"night"`
	Source       string    `json:"source,omitempty"`
	InBedSource  string    `json:"in_bed_source,omitempty"`
	AsleepSecs   int64     `json:"asleep_seconds"`
	NapSecs      int64     `json:"nap_seconds,omitempty"`
	CoreSecs     int64     `json:"core_seconds"`
	DeepSecs     int64     `json:"deep_seconds"`
	REMSecs      int64     `json:"rem_seconds"`
	UnspecSecs   int64     `json:"unspecified_seconds"`
	AwakeSecs    int64     `json:"awake_seconds"`
	InBedSecs    int64     `json:"in_bed_seconds"`
	Efficiency   *float64  `json:"efficiency_pct,omitempty"`
	Awakenings   int       `json:"awakenings"`
	LatencySecs  *int64    `json:"latency_seconds,omitempty"`
	Bedtime      string    `json:"bedtime,omitempty"`
	Wake         string    `json:"wake,omitempty"`
	Midpoint     string    `json:"midpoint,omitempty"`
	StagesSource []SegView `json:"stages,omitempty"`
}

// SegView is one stage row for JSON.
type SegView struct {
	Stage string `json:"stage"`
	Start string `json:"start"` // RFC 3339 with the offset it was recorded in
	End   string `json:"end"`
}

// ---- per-night numbers ------------------------------------------------

// HasStages reports whether the night has real sleep stages (not only
// "asleep, unspecified"). Awake, awakenings, deep, REM and core only mean
// something then.
func (n SleepNight) HasStages() bool { return n.Core+n.Deep+n.REM > 0 }

// Efficiency is time asleep as a percentage of time in bed. Without in_bed
// rows it falls back to asleep / (asleep + awake), which needs awake rows.
// Over 100% (the Watch's sleep outlasts the iPhone's in-bed time) it is
// capped at 100 and clamped reports true. ok is false when it cannot be
// worked out.
func (n SleepNight) Efficiency() (pct float64, clamped, ok bool) {
	var denom time.Duration
	switch {
	case n.Asleep <= 0:
		return 0, false, false
	case n.InBed > 0:
		denom = n.InBed
	case n.Awake > 0:
		denom = n.Asleep - n.Nap + n.Awake
	default:
		return 0, false, false
	}
	// Naps are outside the main night, so they do not count against time in bed.
	pct = float64(n.Asleep-n.Nap) / float64(denom) * 100
	if pct > 100 {
		return 100, true, true
	}
	return pct, false, true
}

// clockMin is the local time of ts as minutes since 18:00, 0..1439. The
// sleep day starts at 18:00 (see nightShift), so 00:30 (390) comes after
// 23:30 (330) and averages and spreads need no wrap-around code.
func clockMin(ts int64, tz int) float64 {
	t := localTime(ts, tz)
	m := t.Hour()*60 + t.Minute()
	return float64((m - 18*60 + 24*60) % (24 * 60))
}

// BedtimeMin, WakeMin and MidpointMin are clock times since 18:00 (see
// clockMin); ok is false when the night has no asleep time.
func (n SleepNight) BedtimeMin() (float64, bool) {
	if n.Start == 0 {
		return 0, false
	}
	return clockMin(n.Start, n.StartTZ), true
}

func (n SleepNight) WakeMin() (float64, bool) {
	if n.Start == 0 {
		return 0, false
	}
	return clockMin(n.End, n.EndTZ), true
}

func (n SleepNight) MidpointMin() (float64, bool) {
	if n.Start == 0 {
		return 0, false
	}
	return math.Mod(clockMin(n.Start, n.StartTZ)+float64(n.End-n.Start)/120, 24*60), true
}

// ClockText writes minutes since 18:00 as a clock time: 312 -> "23:12".
func ClockText(minSince18 float64) string {
	m := (int(math.Round(minSince18)) + 18*60) % (24 * 60)
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

// View converts the night for JSON.
func (n SleepNight) View() *SleepNightView {
	secs := func(d time.Duration) int64 { return int64(d / time.Second) }
	v := &SleepNightView{
		Night: n.Night, Source: n.Source, InBedSource: n.InBedSource,
		AsleepSecs: secs(n.Asleep), CoreSecs: secs(n.Core), DeepSecs: secs(n.Deep),
		REMSecs: secs(n.REM), UnspecSecs: secs(n.Unspecified), AwakeSecs: secs(n.Awake),
		InBedSecs: secs(n.InBed), Awakenings: n.Awakenings, NapSecs: secs(n.Nap),
	}
	if pct, _, ok := n.Efficiency(); ok {
		v.Efficiency = &pct
	}
	if n.Latency > 0 {
		l := secs(n.Latency)
		v.LatencySecs = &l
	}
	if m, ok := n.BedtimeMin(); ok {
		v.Bedtime = ClockText(m)
		w, _ := n.WakeMin()
		v.Wake = ClockText(w)
		mid, _ := n.MidpointMin()
		v.Midpoint = ClockText(mid)
	}
	for _, s := range n.Segments {
		v.StagesSource = append(v.StagesSource, SegView{
			Stage: s.Stage,
			Start: localTime(s.Start, s.TZ).Format(time.RFC3339),
			End:   localTime(s.End, s.TZ).Format(time.RFC3339),
		})
	}
	return v
}

// ---- windows and averages ---------------------------------------------

// lastNights keeps the nights dated within `days` days up to and including
// `last` (a "YYYY-MM-DD" day).
func lastNights(nights []SleepNight, last string, days int) []SleepNight {
	from, err := AddDays(last, -(days - 1))
	if err != nil {
		return nil
	}
	var out []SleepNight
	for _, n := range nights {
		if n.Night >= from && n.Night <= last {
			out = append(out, n)
		}
	}
	return out
}

// mean of f over the nights where f is ok; nil when there are none.
func meanOf(nights []SleepNight, f func(SleepNight) (float64, bool)) *float64 {
	var sum float64
	count := 0
	for _, n := range nights {
		if v, ok := f(n); ok {
			sum += v
			count++
		}
	}
	if count == 0 {
		return nil
	}
	m := sum / float64(count)
	return &m
}

func secsOf(d time.Duration) float64 { return d.Seconds() }

// aggregate averages the nights of one window.
func aggregate(nights []SleepNight) *SleepAgg {
	a := &SleepAgg{Nights: len(nights)}
	a.AsleepSecs = meanOf(nights, func(n SleepNight) (float64, bool) { return secsOf(n.Asleep), n.Asleep > 0 })
	a.InBedSecs = meanOf(nights, func(n SleepNight) (float64, bool) { return secsOf(n.InBed), n.InBed > 0 })
	a.Efficiency = meanOf(nights, func(n SleepNight) (float64, bool) { p, _, ok := n.Efficiency(); return p, ok })
	a.AwakeSecs = meanOf(nights, func(n SleepNight) (float64, bool) { return secsOf(n.Awake), n.HasStages() })
	a.Awakenings = meanOf(nights, func(n SleepNight) (float64, bool) { return float64(n.Awakenings), n.HasStages() })
	a.LatencySecs = meanOf(nights, func(n SleepNight) (float64, bool) { return secsOf(n.Latency), n.Latency > 0 })
	a.DeepSecs = meanOf(nights, func(n SleepNight) (float64, bool) { return secsOf(n.Deep), n.HasStages() })
	a.REMSecs = meanOf(nights, func(n SleepNight) (float64, bool) { return secsOf(n.REM), n.HasStages() })
	a.CoreSecs = meanOf(nights, func(n SleepNight) (float64, bool) { return secsOf(n.Core), n.HasStages() })
	a.BedtimeMin = meanOf(nights, SleepNight.BedtimeMin)
	a.WakeMin = meanOf(nights, SleepNight.WakeMin)
	a.MidpointMin = meanOf(nights, SleepNight.MidpointMin)
	return a
}

// minRegularityNights is the fewest nights with a bedtime for a spread.
const minRegularityNights = 5

// spreadOf is the standard deviation of bedtime and of midpoint over the
// nights, or nil with fewer than minRegularityNights.
func spreadOf(nights []SleepNight) *SleepRegularity {
	var bed, mid []float64
	for _, n := range nights {
		if b, ok := n.BedtimeMin(); ok {
			bed = append(bed, b)
			m, _ := n.MidpointMin()
			mid = append(mid, m)
		}
	}
	if len(bed) < minRegularityNights {
		return nil
	}
	_, bs := meanStddev(bed)
	_, ms := meanStddev(mid)
	return &SleepRegularity{Nights: len(bed), BedtimeMin: bs, MidpointMin: ms}
}

// summarize builds the report from nights (any order is fine, they are
// sorted by SleepNights). usualNights is the size of the usual window.
func summarize(today string, nights []SleepNight, usualNights int, target time.Duration) *SleepReport {
	r := &SleepReport{Today: today, Nights: nights, UsualNights: usualNights}
	if len(nights) == 0 {
		r.Empty = true
		return r
	}
	last := nights[len(nights)-1]
	r.Last = last
	r.Night = last.View()
	if age, err := ageOf(today, last.Night); err == nil {
		r.AgeDays = age
	}
	switch {
	case last.HasStages():
		r.Kind = "stages"
	case last.Asleep > 0:
		r.Kind = "unspecified"
	default:
		r.Kind = "in_bed"
	}

	week := lastNights(nights, last.Night, 7)
	fortnight := lastNights(nights, last.Night, 14)
	usual := lastNights(nights, last.Night, usualNights)
	r.Week, r.Fortnight, r.Usual = aggregate(week), aggregate(fortnight), aggregate(usual)

	if reg := spreadOf(fortnight); reg != nil {
		if len(usual) >= MinHeatmapNights && usualNights > 14 {
			if long := spreadOf(usual); long != nil && long.Nights >= MinHeatmapNights {
				reg.UsualBedtimeMin = long.BedtimeMin
			}
		}
		r.Regularity = reg
	}

	if last.Asleep > 0 && target > 0 {
		d := &SleepDebt{TargetSecs: int64(target / time.Second), LastSecs: int64((target - last.Asleep) / time.Second)}
		for _, n := range week {
			if n.Asleep > 0 {
				d.Nights++
				d.WindowSecs += int64((target - n.Asleep) / time.Second)
			}
		}
		r.Debt = d
	}
	return r
}

// ---- heatmap ------------------------------------------------------------

// Heatmap values.
const (
	SleepByAsleep     = "asleep"
	SleepByBedtime    = "bedtime"
	SleepByEfficiency = "efficiency"
)

// sleepHeatmap lays out one number per night with the generic engine. Only
// nights that have the number are in the map, so the rest show as gaps.
func sleepHeatmap(nights []SleepNight, now time.Time, weeks int, by string) (*Heatmap, error) {
	spec := HeatSpec{}
	var value func(SleepNight) (float64, bool)
	switch by {
	case "", SleepByAsleep:
		spec = HeatSpec{Title: "time asleep", Format: minutesText}
		value = func(n SleepNight) (float64, bool) { return n.Asleep.Minutes(), n.Asleep > 0 }
	case SleepByBedtime:
		spec = HeatSpec{Title: "bedtime", Format: ClockText}
		value = SleepNight.BedtimeMin
	case SleepByEfficiency:
		spec = HeatSpec{Title: "sleep efficiency", Format: func(v float64) string { return fmt.Sprintf("%.0f%%", v) }}
		value = func(n SleepNight) (float64, bool) { p, _, ok := n.Efficiency(); return p, ok }
	default:
		return nil, fmt.Errorf("--by must be asleep, bedtime or efficiency, got %q", by)
	}
	byDay := map[string]float64{}
	for _, n := range nights {
		if v, ok := value(n); ok {
			byDay[n.Night] = v
		}
	}
	h, err := HeatmapFromDays(spec, now, weeks, byDay)
	if err != nil {
		return nil, err
	}
	h.Metric = "sleep_" + map[string]string{"": "asleep", SleepByAsleep: "asleep", SleepByBedtime: "bedtime", SleepByEfficiency: "efficiency"}[by]
	return h, nil
}

// minutesText writes minutes as "7h 05m" (or "45m") for legends.
func minutesText(mins float64) string {
	m := int(math.Round(mins))
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh %02dm", m/60, m%60)
}

// BuildSleepHeatmap is the sleep calendar alone: the last `weeks` weeks, one
// cell per night. by is asleep, bedtime or efficiency.
func BuildSleepHeatmap(ctx context.Context, conn *sql.DB, weeks int, now time.Time, by string) (*Heatmap, error) {
	if weeks < 1 || weeks > MaxHeatmapWeeks {
		return nil, fmt.Errorf("stats: weeks must be between 1 and %d, got %d", MaxHeatmapWeeks, weeks)
	}
	today := now.Format(DayLayout)
	from, err := AddDays(today, -(7*weeks + 6))
	if err != nil {
		return nil, err
	}
	nights, err := SleepNights(ctx, conn, from, today)
	if err != nil {
		return nil, err
	}
	return sleepHeatmap(nights, now, weeks, by)
}

// BuildSleepReport reads the sleep rows once and builds the SLEEP section.
// With no sleep rows it returns a report with Empty set (no error).
func BuildSleepReport(ctx context.Context, conn *sql.DB, now time.Time, opts SleepOptions) (*SleepReport, error) {
	if opts.Nights == 0 {
		opts.Nights = DefaultSleepNights
	}
	if opts.Nights < MinSleepNights || opts.Nights > MaxSleepNights {
		return nil, fmt.Errorf("--nights must be between %d and %d, got %d", MinSleepNights, MaxSleepNights, opts.Nights)
	}
	if opts.Target == 0 {
		opts.Target = DefaultSleepTarget
	}
	if opts.Target < 0 {
		return nil, fmt.Errorf("--sleep-target must be positive, got %s", opts.Target)
	}
	if opts.Weeks < 0 || opts.Weeks > MaxHeatmapWeeks {
		return nil, fmt.Errorf("--weeks must be between 0 and %d, got %d", MaxHeatmapWeeks, opts.Weeks)
	}
	if _, err := sleepHeatmap(nil, now, 1, opts.By); err != nil { // validate --by early
		return nil, err
	}

	today := now.Format(DayLayout)
	// One query covers both the usual window and the calendar.
	days := max(opts.Nights, 7*opts.Weeks+6)
	from, err := AddDays(today, -(days - 1))
	if err != nil {
		return nil, err
	}
	nights, err := SleepNights(ctx, conn, from, today)
	if err != nil {
		return nil, err
	}
	r := summarize(today, nights, opts.Nights, opts.Target)
	if opts.Weeks > 0 && !r.Empty {
		h, err := sleepHeatmap(nights, now, opts.Weeks, opts.By)
		if err != nil {
			return nil, err
		}
		if !h.Empty() {
			r.Heatmap = h
		}
	}
	return r, nil
}
