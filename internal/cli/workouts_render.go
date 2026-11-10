package cli

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/nmutto/healthcli/internal/stats"
)

// This file draws the WORKOUTS section. renderWorkoutsSection is pure (a
// report in, lines out), so tests render it at any width without a database.
// "overview" shows the compact form; "healthcli workouts" shows everything.

// workoutsOpts picks what the section shows.
type workoutsOpts struct {
	Full   bool // also weekly bars, bests and the heatmap
	Weeks  int  // heatmap length in weeks (0: as many weeks as the report has)
	ByType bool // heatmap cells are type letters instead of shades
	// NoHeatmap leaves the heatmap out of the full section (the caller draws it).
	NoHeatmap bool
}

const (
	wkMaxTypeRows = 6  // more activity types than this are summarised in one line
	wkMaxBar      = 20 // the longest share / volume bar
	wkMinHeatRuns = 4  // fewer sessions than this: no heatmap (it would be mostly rest)
)

// renderWorkoutsSection returns the lines of the section, or one dim line when
// there is nothing to show. width is the room to use (60..100).
func renderWorkoutsSection(r *stats.WorkoutsReport, ti termInfo, width int, opts workoutsOpts) []string {
	g := glyphsFor(ti.unicode)
	st := style{color: ti.color}
	if r == nil || r.Count == 0 {
		days := 28
		if r != nil {
			days = r.Days
		}
		text := fmt.Sprintf("none in the last %d days", days)
		if r != nil && r.Last != "" {
			text = fmt.Sprintf("none in %d d (last: %s)", days, dayText(r.Last, "Jan 02"))
		}
		return []string{indent + bandLine("WORKOUTS", text, width-len(indent)-1, g, st)}
	}

	total := width - len(indent) - 1
	lines := []string{indent + bandLine("WORKOUTS", wkSummary(r, bandRoom("WORKOUTS", total)), total, g, st), ""}
	lines = append(lines, wkTypeTable(r, g, st, width)...)
	lines = append(lines, "")
	lines = append(lines, wkRecent(r, g, st, width, opts.Full)...)
	if !opts.Full {
		return lines
	}

	shown := false
	if wkWeeksWorthDrawing(r) {
		lines = append(lines, "")
		lines = append(lines, wkWeekly(r, g, st, width)...)
		shown = true
	}
	if len(r.Bests) > 0 {
		lines = append(lines, "")
		lines = append(lines, wkBests(r, st)...)
		shown = true
	}
	if opts.NoHeatmap {
		return lines
	}
	if r.Count >= wkMinHeatRuns {
		if h := wkHeatmap(r, ti, width, opts); len(h) > 0 {
			lines = append(lines, "")
			lines = append(lines, h...)
		}
	}
	if r.Count < wkMinHeatRuns {
		note := "(weekly volume, bests and the heatmap need more sessions)"
		if shown {
			note = "(the heatmap needs more sessions)"
		}
		lines = append(lines, "")
		lines = append(lines, wrapDim(note, width, st)...)
	}
	return lines
}

// wkSummary is the total in the section band: "14 sessions  12h 40m  8,420 kcal ...
// active 12 of 28 d". Parts that do not fit are dropped from the middle; kcal
// and km only appear when something recorded them.
func wkSummary(r *stats.WorkoutsReport, room int) string {
	parts := []string{
		plural(int64(r.Count), "session"),
		formatDuration(wkDur(r.DurationSeconds)),
	}
	if r.EnergyKcal != nil {
		parts = append(parts, formatInt(*r.EnergyKcal)+" kcal")
	}
	if r.DistanceKM != nil {
		parts = append(parts, fmt.Sprintf("%.1f km", *r.DistanceKM))
	}
	parts = append(parts, fmt.Sprintf("active %d of %d d", r.ActiveDays, r.Days))
	for len(parts) > 2 && runeLen(strings.Join(parts, "  ")) > room {
		parts = append(parts[:len(parts)-2], parts[len(parts)-1]) // drop the one before "active"
		if runeLen(strings.Join(parts, "  ")) > room && len(parts) > 2 {
			parts = parts[:len(parts)-1] // still too long: drop "active" too
		}
	}
	return strings.Join(parts, "  ")
}

// wkTypeTable is the TYPE / N / TIME / KCAL / KM / share table. A column that
// is empty for every row (no kcal recorded at all) is left out.
func wkTypeTable(r *stats.WorkoutsReport, g glyphSet, st style, width int) []string {
	rows := r.ByType
	extra := 0
	if len(rows) > wkMaxTypeRows {
		extra = len(rows) - wkMaxTypeRows
		rows = rows[:wkMaxTypeRows]
	}
	nameW := 4
	for _, t := range rows {
		nameW = max(nameW, min(runeLen(t.Activity), 12))
	}
	hasKcal, hasKM := false, false
	for _, t := range rows {
		hasKcal = hasKcal || t.EnergyKcal != nil
		hasKM = hasKM || t.DistanceKM != nil
	}

	// Width of everything except the bar: indent, name, N, TIME and the
	// percentage. The bar gets what is left (at most wkMaxBar); if even 6
	// cells do not fit, the KM column goes first, then KCAL.
	fixed := func() int {
		w := len(indent) + nameW + 1 + 3 + 1 + 7 + 1 + 5 // 5: " 100%"
		if hasKcal {
			w += 1 + 7
		}
		if hasKM {
			w += 1 + 6
		}
		return w + 1
	}
	if width-fixed() < 6 {
		hasKM = false
	}
	if width-fixed() < 6 {
		hasKcal = false
	}
	barW := min(width-fixed(), wkMaxBar)

	head := indent + padRight("TYPE", nameW) + " " + fmt.Sprintf("%3s", "N") + " " + padRight("TIME", 7)
	if hasKcal {
		head += " " + fmt.Sprintf("%7s", "KCAL")
	}
	if hasKM {
		head += " " + fmt.Sprintf("%6s", "KM")
	}
	if barW >= 6 {
		head += " SHARE"
	}
	lines := []string{st.dim(head)}
	for _, t := range rows {
		line := indent + padRight(fit(t.Activity, nameW, g), nameW) + " " + fmt.Sprintf("%3d", t.Count) + " " +
			padRight(formatDuration(wkDur(t.DurationSeconds)), 7)
		if hasKcal {
			line += " " + fmt.Sprintf("%7s", wkOrDash(t.EnergyKcal, func(v float64) string { return formatInt(v) }))
		}
		if hasKM {
			line += " " + fmt.Sprintf("%6s", wkOrDash(t.DistanceKM, func(v float64) string { return fmt.Sprintf("%.1f", v) }))
		}
		if barW >= 6 {
			share := t.DurationSeconds / r.DurationSeconds
			line += " " + wkBar(share, barW, g) + fmt.Sprintf(" %3d%%", int(math.Round(share*100)))
		}
		lines = append(lines, line)
	}
	if extra > 0 {
		lines = append(lines, indent+st.dim(fmt.Sprintf("+%d more", extra)))
	}
	return lines
}

func wkOrDash(v *float64, format func(float64) string) string {
	if v == nil {
		return "-"
	}
	return format(*v)
}

// wkBar is a bar of `width` cells, filled in proportion to frac (0..1). A
// non-zero value always gets at least one filled cell.
func wkBar(frac float64, width int, g glyphSet) string {
	n := int(math.Round(frac * float64(width)))
	if frac > 0 {
		n = max(n, 1)
	}
	n = min(max(n, 0), width)
	return strings.Repeat(string(g.stages["deep"]), n) + strings.Repeat(string(g.stages["awake"]), width-n)
}

// wkRecent lists the newest sessions. Columns (distance, pace, kcal) that no
// shown row has are dropped, and the rightmost go first when the line is too wide.
func wkRecent(r *stats.WorkoutsReport, g glyphSet, st style, width int, full bool) []string {
	n := 3
	if full {
		n = 5
	}
	if width < 70 {
		n = min(n, 2)
	}
	rows := r.Recent[:min(n, len(r.Recent))]
	return wkSessionRows(rows, "RECENT", 10, func(w stats.Workout) string { return w.Start.Format("Mon Jan 02") }, g, st, width)
}

// wkSessionRows draws sessions as a title line and one row each:
// when, activity, time, then distance, pace and kcal where they fit. whenW is
// the width of the `when` text (a date for the recent list, a clock time for a
// single day). "healthcli today" uses it too.
func wkSessionRows(rows []stats.Workout, title string, whenW int, when func(stats.Workout) string, g glyphSet, st style, width int) []string {
	if len(rows) == 0 {
		return nil
	}
	nameW := 4
	for _, w := range rows {
		nameW = max(nameW, min(runeLen(w.Activity), 12))
	}
	type cell struct{ dist, pace, kcal string }
	cells := make([]cell, len(rows))
	has := [3]bool{}
	for i, w := range rows {
		if w.DistanceM != nil && *w.DistanceM > 0 { // a recorded 0 m is no distance
			cells[i].dist = fmt.Sprintf("%.1f km", *w.DistanceM/1000)
			cells[i].pace = wkPace(w)
		}
		if w.EnergyKcal != nil {
			cells[i].kcal = formatInt(*w.EnergyKcal) + " kcal"
		}
		has[0] = has[0] || cells[i].dist != ""
		has[1] = has[1] || cells[i].pace != ""
		has[2] = has[2] || cells[i].kcal != ""
	}
	// when, name, time 7, dist 7, pace 9, kcal 8, with spaces between.
	room := width - len(indent) - whenW - 1 - nameW - 1 - 7
	for _, c := range []struct {
		i, w int
	}{{2, 9}, {1, 10}, {0, 8}} { // drop order: kcal, pace, dist
		need := 0
		if has[0] {
			need += 8
		}
		if has[1] {
			need += 10
		}
		if has[2] {
			need += 9
		}
		if need <= room {
			break
		}
		has[c.i] = false
	}

	lines := []string{indent + st.dim(title)}
	for i, w := range rows {
		line := indent + padRight(when(w), whenW) + " " + padRight(fit(w.Activity, nameW, g), nameW) + " " +
			fmt.Sprintf("%7s", formatDuration(w.Duration))
		if has[0] {
			line += " " + fmt.Sprintf("%7s", cells[i].dist)
		}
		if has[1] {
			line += " " + padRight(cells[i].pace, 9)
		}
		if has[2] {
			line += " " + fmt.Sprintf("%8s", cells[i].kcal)
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return lines
}

// wkPace is the average pace ("5:59 /km") or, for cycling, speed ("20.0 km/h").
// It is an average over the whole session; a short track gives no pace.
func wkPace(w stats.Workout) string {
	if w.DistanceM == nil || *w.DistanceM < 1000 || w.Duration <= 0 {
		return ""
	}
	km := *w.DistanceM / 1000
	if w.Activity == "cycling" {
		return fmt.Sprintf("%.1f km/h", km/w.Duration.Hours())
	}
	return paceText(w.Duration.Seconds() / km)
}

func paceText(secPerKM float64) string {
	s := int(math.Round(secPerKM))
	return fmt.Sprintf("%d:%02d /km", s/60, s%60)
}

// wkWeeksWorthDrawing: a bar chart of fewer than 3 weeks with a session is noise.
func wkWeeksWorthDrawing(r *stats.WorkoutsReport) bool {
	n := 0
	for _, w := range r.Weeks {
		if w.Count > 0 {
			n++
		}
	}
	return n >= 3
}

// wkWeekly draws one bar per week, scaled so the busiest week fills the bar.
// The current week is marked "(3 d)" and gets "so far" instead of a delta.
func wkWeekly(r *stats.WorkoutsReport, g glyphSet, st style, width int) []string {
	const labelW, durW, countW, deltaW = 7, 12, 3, 8
	fixed := len(indent) + labelW + 1 + 1 + durW + 1 + countW + 1 + deltaW
	showDelta := width-fixed >= 10
	if !showDelta {
		fixed = len(indent) + labelW + 1 + 1 + durW
	}
	barW := min(width-fixed, wkMaxBar+4)
	top := 0.0
	for _, w := range r.Weeks {
		top = math.Max(top, w.DurationSeconds)
	}

	head := indent + padRight("WEEK OF", labelW) + " " + padRight("TIME", barW+1+durW)
	if showDelta {
		head += " " + fmt.Sprintf("%*s", countW, "N") + " " + "VS AVG"
	}
	lines := []string{st.dim(strings.TrimRight(head, " "))}
	for _, w := range r.Weeks {
		frac := 0.0
		if top > 0 {
			frac = w.DurationSeconds / top
		}
		dur := formatDuration(wkDur(w.DurationSeconds))
		if w.Partial {
			dur += fmt.Sprintf(" (%d d)", w.Days)
		}
		line := indent + padRight(dayText(w.Start, "Jan 02"), labelW) + " " + wkBar(frac, barW, g) + " " + padRight(dur, durW)
		if showDelta {
			delta := ""
			switch {
			case w.Partial:
				delta = g.flat + " so far"
			case w.DeltaPct != nil:
				arrow := g.deltaArrow(*w.DeltaPct)
				delta = arrow + " " + fmt.Sprintf("%+.0f%%", *w.DeltaPct)
			}
			line += " " + fmt.Sprintf("%*d", countW, w.Count) + " " + delta
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return lines
}

// wkBests lists the personal bests (activities with 3+ sessions in 90 days).
func wkBests(r *stats.WorkoutsReport, st style) []string {
	lines := []string{indent + st.dim(fmt.Sprintf("BESTS (%d d)", stats.BestsDays))}
	for _, b := range r.Bests {
		var what string
		switch b.Kind {
		case "longest":
			what = formatDuration(wkDur(b.DurationSeconds))
		case "furthest":
			what = fmt.Sprintf("%.1f km", *b.DistanceKM)
		case "biggest":
			what = formatInt(*b.EnergyKcal) + " kcal"
		case "fastest":
			if b.SpeedKMH > 0 {
				what = fmt.Sprintf("%.1f km/h", b.SpeedKMH)
			} else {
				what = paceText(b.PaceSecPerKM)
			}
		}
		line := indent + padRight(b.Kind, 9) + " " + b.Activity + " " + what + ", " + dayText(b.Day, "Jan 02")
		if b.New {
			line += " " + st.bold("*new*")
		}
		lines = append(lines, line)
	}
	return lines
}

// wkHeatmap draws the workout-minutes calendar (the shared heatmap engine),
// with optional type letters. Returns nil when it cannot be built.
func wkHeatmap(r *stats.WorkoutsReport, ti termInfo, width int, opts workoutsOpts) []string {
	now, err := time.Parse(stats.DayLayout, r.Today)
	if err != nil {
		return nil
	}
	weeks := opts.Weeks
	if weeks <= 0 {
		weeks = max(len(r.Weeks), 1)
	}
	room := width - len(indent)
	weeks = min(weeks, stats.MaxHeatmapWeeks, heatWeeksFit(room))
	spec := stats.HeatSpec{Title: "workout minutes", Hue: stats.HueGreen, Format: minutesText, ZeroIsRest: true}
	h, err := stats.HeatmapFromDays(spec, now, weeks, r.MinutesByDay)
	if err != nil || h.Empty() {
		return nil
	}
	sub := ti
	sub.width = room
	var lines []string
	if opts.ByType {
		lines = renderTypeHeatmap(h, r, sub)
	} else {
		lines = renderHeatmap(h, sub)
	}
	for i, l := range lines {
		if l != "" {
			lines[i] = indent + l
		}
	}
	return lines
}

// typeLetters gives every activity its own letter: the first letter of its
// name that is not taken yet (running R, rowing O, ...), in the report's order
// (most time first), then any activity only seen in the heatmap window.
func typeLetters(r *stats.WorkoutsReport) (order []string, letters map[string]rune) {
	seen := map[string]bool{}
	for _, t := range r.ByType {
		order = append(order, t.Activity)
		seen[t.Activity] = true
	}
	var more []string
	for _, a := range r.TopTypeByDay {
		if !seen[a] {
			seen[a] = true
			more = append(more, a)
		}
	}
	sort.Strings(more) // map order is random; sort so the output is stable
	order = append(order, more...)

	letters = map[string]rune{}
	used := map[rune]bool{}
	for _, a := range order {
		letter := '?'
		for _, c := range strings.ToUpper(a) {
			if c >= 'A' && c <= 'Z' && !used[c] {
				letter = c
				break
			}
		}
		used[letter] = true
		letters[a] = letter
	}
	return order, letters
}

// typeColors cycle over the activities; the letter itself says which is which.
var typeColors = []string{"36", "34", "32", "35", "33"}

// renderTypeHeatmap is renderHeatmap with a letter per day instead of a
// shade: the day's main activity. UPPERCASE is a top-two-quintile day.
func renderTypeHeatmap(h *stats.Heatmap, r *stats.WorkoutsReport, ti termInfo) []string {
	g := glyphsFor(ti.unicode)
	st := style{color: ti.color}
	weeks := h.Weeks
	if n := heatWeeksFit(ti.width); len(weeks) > n {
		weeks = weeks[len(weeks)-n:]
	}
	order, letters := typeLetters(r)
	colorOf := map[string]string{}
	for i, a := range order {
		colorOf[a] = typeColors[i%len(typeColors)]
	}

	name := heatTitle(h) + " per day, by type"
	total := ti.width - 1
	title := name
	for _, cand := range []string{fmt.Sprintf("%s, %s to %s", name, plural(int64(len(weeks)), "week"), h.Today), name} {
		title = cand
		if runeLen(cand) <= bandRoom("CALENDAR", total) {
			break
		}
	}
	lines := []string{
		bandLine("CALENDAR", title, total, g, st),
		heatMonthLine(weeks),
	}
	for d := 0; d < 7; d++ {
		var b strings.Builder
		b.WriteString(heatDayNames[d] + " ")
		marker := ""
		for _, week := range weeks {
			cell := week[d]
			switch {
			case cell.Future:
				b.WriteString("  ")
				continue
			case cell.Value == nil:
				b.WriteString(" ") // before the first workout: blank
			case cell.Level == 0:
				b.WriteString(st.dim(string(restGlyph(g))))
			default:
				act := r.TopTypeByDay[cell.Day]
				letter := letters[act]
				if letter == 0 {
					letter = '?'
				}
				s := string(letter)
				if cell.Level < stats.HeatLevels-1 {
					s = strings.ToLower(s)
				}
				b.WriteString(st.wrap(colorOf[act], s))
			}
			b.WriteString(" ")
			if cell.Day == h.Today {
				marker = todayMark(ti.unicode)
			}
		}
		line := strings.TrimRight(b.String(), " ")
		if marker != "" {
			line += " " + st.bold(marker)
		}
		lines = append(lines, line)
	}

	var legend []string
	for _, a := range order {
		if letters[a] != '?' {
			legend = append(legend, st.wrap(colorOf[a], string(letters[a]))+" "+a)
		}
	}
	lines = append(lines, "")
	lines = append(lines, wrapParts(legend, "  ", ti.width)...)
	tail := []string{st.dim(string(restGlyph(g))) + " rest day", todayMark(ti.unicode) + " today"}
	used := heatLevelsUsed(weeks)
	if used[stats.HeatLevels-1] || used[stats.HeatLevels] {
		tail = append(tail, "UPPERCASE = longer days") // only when a day can be uppercase
	}
	lines = append(lines, wrapParts(tail, "   ", ti.width)...)
	return lines
}

// dayText rewrites "2026-11-07" with a Go time layout ("Jan 02"); on a bad
// date it returns the input unchanged.
func dayText(day, layout string) string {
	t, err := time.Parse(stats.DayLayout, day)
	if err != nil {
		return day
	}
	return t.Format(layout)
}

// wkDur turns seconds from the report into a Duration for formatDuration.
func wkDur(seconds float64) time.Duration { return time.Duration(seconds * float64(time.Second)) }
