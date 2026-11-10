package stats

import (
	"context"
	"database/sql"
	"sort"
)

// This file builds the WORKOUTS report. Everything is derived from one call to
// Workouts(), so the de-duplication rules (Watch beats iPhone) are the same as
// everywhere else. Nothing here prints: the cli package draws the report.

const (
	// BestsDays is how far back "bests" look. A best needs history, so it is
	// longer than the normal 28-day window.
	BestsDays = 90
	// bestMinSessions: one run is trivially "the fastest", so an activity needs
	// at least this many sessions in the bests window to appear there.
	bestMinSessions = 3
	// paceMinMeters: very short GPS tracks give silly paces, so a "fastest" needs this.
	paceMinMeters = 3000
	// recentCount is how many sessions the recent list shows.
	recentCount = 5
)

// WorkoutsReport is the WORKOUTS section. kcal and km totals are nil when no
// session in the window recorded them (nil is "not recorded", not 0).
type WorkoutsReport struct {
	Today           string       `json:"today"`
	Days            int          `json:"days"`
	Count           int          `json:"count"`
	DurationSeconds float64      `json:"duration_seconds"`
	EnergyKcal      *float64     `json:"energy_kcal,omitempty"`
	DistanceKM      *float64     `json:"distance_km,omitempty"`
	ActiveDays      int          `json:"active_days"`
	ByType          []TypeTotal  `json:"by_type"`
	Weeks           []WeekVolume `json:"weeks"`
	Bests           []Best       `json:"bests,omitempty"`
	Recent          []Workout    `json:"recent"`
	// Last is the newest day with any workout ever read, set only when the
	// window itself is empty ("none in 28 d (last: Sep 02)").
	Last string `json:"last,omitempty"`

	// For the heatmap (not part of the JSON): minutes per day, and the activity
	// with the most minutes on each day.
	MinutesByDay map[string]float64 `json:"-"`
	TopTypeByDay map[string]string  `json:"-"`
}

// TypeTotal is one activity's totals in the window.
type TypeTotal struct {
	Activity        string   `json:"activity"`
	Count           int      `json:"count"`
	DurationSeconds float64  `json:"duration_seconds"`
	EnergyKcal      *float64 `json:"energy_kcal,omitempty"`
	DistanceKM      *float64 `json:"distance_km,omitempty"`
}

// WeekVolume is one Monday-to-Sunday week of training.
type WeekVolume struct {
	Start           string  `json:"start"` // the Monday
	DurationSeconds float64 `json:"duration_seconds"`
	Count           int     `json:"count"`
	// Partial is true for the current week: Days says how many of its days
	// have happened. A partial week never gets a delta.
	Partial bool `json:"partial,omitempty"`
	Days    int  `json:"days"`
	// DeltaPct compares with the average of the up to 4 full weeks before it;
	// nil when fewer than 2 such weeks exist or the average is 0.
	DeltaPct *float64 `json:"delta_pct,omitempty"`
}

// Best is one personal best. Kind is longest, furthest, biggest or fastest.
type Best struct {
	Kind            string   `json:"kind"`
	Activity        string   `json:"activity"`
	Day             string   `json:"day"`
	DurationSeconds float64  `json:"duration_seconds"`
	DistanceKM      *float64 `json:"distance_km,omitempty"`
	EnergyKcal      *float64 `json:"energy_kcal,omitempty"`
	PaceSecPerKM    float64  `json:"pace_sec_per_km,omitempty"` // fastest running/walking
	SpeedKMH        float64  `json:"speed_kmh,omitempty"`       // fastest cycling
	New             bool     `json:"new,omitempty"`             // set inside the report window
}

// BuildWorkoutsReport summarises the last `days` days ending on `today`, a
// `weeks`-week volume chart and the 90-day bests, all from one read.
func BuildWorkoutsReport(ctx context.Context, conn *sql.DB, today string, days, weeks int) (*WorkoutsReport, error) {
	return BuildWorkoutsReportWithHeatmap(ctx, conn, today, days, weeks, weeks)
}

// BuildWorkoutsReportWithHeatmap is BuildWorkoutsReport for a heatmap that
// is `heatWeeks` weeks long, which can be longer than the volume chart (the
// overview draws an 8-week chart next to a heatmap of up to a year).
func BuildWorkoutsReportWithHeatmap(ctx context.Context, conn *sql.DB, today string, days, weeks, heatWeeks int) (*WorkoutsReport, error) {
	// Read far enough back for every part: bests, the weekly chart (plus the
	// weeks its deltas compare with) and the heatmap (which starts on the
	// Monday heatWeeks calendar weeks back).
	back := max(BestsDays, days, 7*(weeks+5), 7*heatWeeks+6)
	from, err := AddDays(today, -(back - 1))
	if err != nil {
		return nil, err
	}
	all, err := Workouts(ctx, conn, from, today)
	if err != nil {
		return nil, err
	}
	winFrom, err := AddDays(today, -(days - 1))
	if err != nil {
		return nil, err
	}
	r := &WorkoutsReport{Today: today, Days: days, ByType: []TypeTotal{}, Weeks: []WeekVolume{}, Bests: []Best{}, Recent: []Workout{},
		MinutesByDay: map[string]float64{}, TopTypeByDay: map[string]string{}}

	var window []Workout
	for _, w := range all {
		if w.Day >= winFrom {
			window = append(window, w)
		}
	}
	if len(window) == 0 {
		if len(all) > 0 {
			r.Last = all[len(all)-1].Day
		}
		return r, nil
	}

	r.fillTotals(window)
	r.fillDays(all)
	r.Weeks = weeklyVolume(all, today, weeks)
	r.Bests = bests(all, today, winFrom)
	for i := len(window) - 1; i >= 0 && len(r.Recent) < recentCount; i-- {
		r.Recent = append(r.Recent, window[i])
	}
	return r, nil
}

// addPtr adds v to *p, creating it on first use, so nil stays "never recorded".
func addPtr(p **float64, v *float64) {
	if v == nil {
		return
	}
	if *p == nil {
		*p = new(float64)
	}
	**p += *v
}

func kmPtr(m *float64) *float64 {
	if m == nil {
		return nil
	}
	km := *m / 1000
	return &km
}

func (r *WorkoutsReport) fillTotals(window []Workout) {
	byType := map[string]*TypeTotal{}
	active := map[string]bool{}
	var meters *float64
	for _, w := range window {
		r.Count++
		r.DurationSeconds += w.Duration.Seconds()
		addPtr(&r.EnergyKcal, w.EnergyKcal)
		addPtr(&meters, w.DistanceM)
		active[w.Day] = true
		t := byType[w.Activity]
		if t == nil {
			t = &TypeTotal{Activity: w.Activity}
			byType[w.Activity] = t
		}
		t.Count++
		t.DurationSeconds += w.Duration.Seconds()
		addPtr(&t.EnergyKcal, w.EnergyKcal)
		addPtr(&t.DistanceKM, w.DistanceM) // metres for now, converted below
	}
	r.DistanceKM = kmPtr(meters)
	r.ActiveDays = len(active)
	for _, t := range byType {
		t.DistanceKM = kmPtr(t.DistanceKM)
		r.ByType = append(r.ByType, *t)
	}
	// Most time first; the name breaks ties so the order never wobbles.
	sort.Slice(r.ByType, func(a, b int) bool {
		x, y := r.ByType[a], r.ByType[b]
		if x.DurationSeconds != y.DurationSeconds {
			return x.DurationSeconds > y.DurationSeconds
		}
		return x.Activity < y.Activity
	})
}

// fillDays prepares the heatmap input: minutes per day and each day's main activity.
func (r *WorkoutsReport) fillDays(all []Workout) {
	perDayType := map[string]map[string]float64{}
	for _, w := range all {
		r.MinutesByDay[w.Day] += w.Duration.Minutes()
		if perDayType[w.Day] == nil {
			perDayType[w.Day] = map[string]float64{}
		}
		perDayType[w.Day][w.Activity] += w.Duration.Minutes()
	}
	for day, types := range perDayType {
		best, bestMin := "", -1.0
		for a, m := range types { // ties: the alphabetically first, so output is stable
			if m > bestMin || (m == bestMin && a < best) {
				best, bestMin = a, m
			}
		}
		r.TopTypeByDay[day] = best
	}
}

// mondayOf returns the Monday of the week that holds day.
func mondayOf(day string) string {
	t, err := parseDay(day)
	if err != nil {
		return day
	}
	m, _ := AddDays(day, -((int(t.Weekday()) + 6) % 7))
	return m
}

// weeklyVolume returns the last `weeks` weeks ending with the week of today.
// Weeks without a session are kept (a zero week is information) once the
// first workout has happened. The deltas look at weeks before the first shown
// one too, which is why all is longer.
func weeklyVolume(all []Workout, today string, weeks int) []WeekVolume {
	byWeek := map[string]*WeekVolume{}
	for _, w := range all {
		m := mondayOf(w.Day)
		v := byWeek[m]
		if v == nil {
			v = &WeekVolume{Start: m, Days: 7}
			byWeek[m] = v
		}
		v.DurationSeconds += w.Duration.Seconds()
		v.Count++
	}
	// Weeks before the first workout are not "zero weeks": they are left out,
	// and a week needs 3 weeks since the first workout to get a delta at all.
	first := ""
	for start := range byWeek {
		if first == "" || start < first {
			first = start
		}
	}
	thisMonday := mondayOf(today)
	var out []WeekVolume
	for i := weeks + 4; i >= 0; i-- { // 4 extra weeks only feed the deltas
		start, err := AddDays(thisMonday, -7*i)
		if err != nil {
			continue
		}
		if start < first {
			continue
		}
		v := WeekVolume{Start: start, Days: 7}
		if got := byWeek[start]; got != nil {
			v = *got
		}
		if start == thisMonday {
			v.Partial = true
			t, _ := parseDay(today)
			v.Days = (int(t.Weekday())+6)%7 + 1
		}
		out = append(out, v)
	}
	for i := range out {
		if out[i].Partial {
			continue
		}
		var sum float64
		n := 0
		for j := max(i-4, 0); j < i; j++ {
			sum += out[j].DurationSeconds
			n++
		}
		if n >= 3 && sum > 0 {
			d := (out[i].DurationSeconds/(sum/float64(n)) - 1) * 100
			out[i].DeltaPct = &d
		}
	}
	return out[max(len(out)-weeks, 0):]
}

// bests picks the personal bests among activities with enough sessions.
func bests(all []Workout, today, winFrom string) []Best {
	from, err := AddDays(today, -(BestsDays - 1))
	if err != nil {
		return nil
	}
	count := map[string]int{}
	var pool []Workout
	for _, w := range all {
		if w.Day >= from {
			count[w.Activity]++
			pool = append(pool, w)
		}
	}
	var qualified []Workout
	for _, w := range pool {
		if count[w.Activity] >= bestMinSessions {
			qualified = append(qualified, w)
		}
	}

	mk := func(kind string, w Workout) Best {
		return Best{Kind: kind, Activity: w.Activity, Day: w.Day, DurationSeconds: w.Duration.Seconds(),
			DistanceKM: kmPtr(w.DistanceM), EnergyKcal: w.EnergyKcal, New: w.Day >= winFrom}
	}
	// pick returns the session with the largest score (score <= 0: not eligible).
	pick := func(score func(Workout) float64) (Workout, bool) {
		var best Workout
		top := 0.0
		for _, w := range qualified { // oldest first, so a tie keeps the older one
			if s := score(w); s > top {
				best, top = w, s
			}
		}
		return best, top > 0
	}

	var out []Best
	if w, ok := pick(func(w Workout) float64 { return w.Duration.Seconds() }); ok {
		out = append(out, mk("longest", w))
	}
	for _, act := range []string{"running", "walking", "cycling"} {
		w, ok := pick(func(w Workout) float64 {
			if w.Activity != act || w.DistanceM == nil || *w.DistanceM < paceMinMeters || w.Duration <= 0 {
				return 0
			}
			return *w.DistanceM / w.Duration.Seconds() // metres per second: faster is bigger
		})
		if !ok {
			continue
		}
		b := mk("fastest", w)
		if act == "cycling" {
			b.SpeedKMH = *w.DistanceM / 1000 / w.Duration.Hours()
		} else {
			b.PaceSecPerKM = w.Duration.Seconds() / (*w.DistanceM / 1000)
		}
		out = append(out, b)
		break // one "fastest" line: the first activity (running, then walking, then cycling) that has one
	}
	if w, ok := pick(func(w Workout) float64 {
		if w.DistanceM == nil {
			return 0
		}
		return *w.DistanceM
	}); ok {
		out = append(out, mk("furthest", w))
	}
	if w, ok := pick(func(w Workout) float64 {
		if w.EnergyKcal == nil {
			return 0
		}
		return *w.EnergyKcal
	}); ok {
		out = append(out, mk("biggest", w))
	}
	return out
}
