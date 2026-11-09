package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/nmutto/healthcli/internal/stats"
)

// This file turns a todayReport into the text "healthcli today" prints: a
// one-day slice of the overview. It reuses the overview's pieces (headline,
// category bands, gutter marker, verdict colours, the sleep and workouts
// sections) and only adds what is different: the USUAL column instead of the
// trend, and the one-day workouts list. It does no I/O, so a test can render a
// report at any width.

// renderToday draws the page. v carries --category and --compact (and now,
// for "yesterday").
func renderToday(r *todayReport, ti termInfo, v overviewView) string {
	g := glyphsFor(ti.unicode)
	st := style{color: ti.color}
	var b strings.Builder

	b.WriteString(dayTitleLine(r.Date, v.now, ti, g, st))
	b.WriteString(indent + strings.Repeat(string(g.rule), ti.width-2) + "\n")

	if r.Status == statusNoData || r.day == nil {
		b.WriteString("\n" + emptyBox(ti, g, st))
		return b.String()
	}
	if r.Status == statusFallback {
		note := fmt.Sprintf("No data for %s; showing %s, the latest day with data", r.RequestedDate, r.Date)
		b.WriteString(strings.Join(wrapDim(note, ti.width, st), "\n") + "\n") // wrapped, never cut
	}

	if h := headline(dayOverview(r.day), v, g, ti.width-2); h != "" {
		b.WriteString(indent + h + "\n")
	}

	if idx := dayIndexLine(r.day, v, ti, g); idx != "" {
		b.WriteString(indent + idx + "\n")
	}

	var blocks []string
	if table := dayTable(r.day, ti, g, st, v); table != "" {
		blocks = append(blocks, "\n"+table+dayFooter(r.day, ti, g, st))
	}
	if v.wants(sleepName) && r.day.Sleep != nil {
		blocks = append(blocks, linesBlock(renderSleepSection(r.day.Sleep, ti, ti.width, sleepOpts{})))
	}
	if v.wants(workoutsName) && len(r.day.Workouts) > 0 {
		blocks = append(blocks, linesBlock(dayWorkouts(r.day.Workouts, ti, g, st)))
	}
	if len(blocks) == 0 { // --category picked only things this day does not have
		b.WriteString("\n" + indent + st.dim("Nothing to show for these categories on this day.") + "\n")
	}
	for i, text := range blocks {
		if v.compact && i > 0 {
			text = strings.TrimPrefix(text, "\n")
		}
		b.WriteString(text)
	}
	return b.String()
}

// dayOverview shapes a day as a stats.Overview, which is what the overview's
// headline reads. Workouts stay empty: the headline would call them "in 30 d".
func dayOverview(d *stats.DayReport) *stats.Overview {
	o := &stats.Overview{Metrics: d.Metrics, Categories: d.Categories, Sleep: stats.OverviewSleep{State: stats.StateNoData}}
	if d.Sleep != nil {
		o.Sleep = stats.OverviewSleep{State: stats.StateOK, Night: d.Day, AsleepSecs: int64(d.Sleep.Last.Asleep / time.Second)}
	}
	return o
}

// dayTitleLine is "Sunday 2026-11-08" in bold, then dim "(yesterday)" or
// "(3 days ago)" when it is not today.
func dayTitleLine(day string, now time.Time, ti termInfo, g glyphSet, st style) string {
	t, err := time.Parse(stats.DayLayout, day)
	if err != nil {
		return indent + st.bold(day) + "\n"
	}
	title := t.Weekday().String() + " " + day
	line := indent + st.bold(title)
	if !now.IsZero() {
		if ago := daysAgoText(day, now); ago != "" {
			line += " " + st.dim(fit("("+ago+")", ti.width-len(indent)-runeLen(title)-1, g))
		}
	}
	return line + "\n"
}

// daysAgoText is "yesterday" or "N days ago"; "" for today (or a later day).
func daysAgoText(day string, now time.Time) string {
	t, err := time.Parse(stats.DayLayout, day)
	if err != nil {
		return ""
	}
	today, _ := time.Parse(stats.DayLayout, now.Format(stats.DayLayout))
	switch n := int(today.Sub(t) / (24 * time.Hour)); {
	case n == 1:
		return "yesterday"
	case n > 1:
		return fmt.Sprintf("%d days ago", n)
	}
	return ""
}

// dayIndexLine is the overview's index line for one day: the categories with
// rows and "!n" for the worse ones, then SLEEP and WORKOUTS when the day has
// them. There is no "not tracked" tail: that would be wrong for a single day.
func dayIndexLine(d *stats.DayReport, v overviewView, ti termInfo, g glyphSet) string {
	var parts []string
	for _, c := range d.Categories {
		if !v.wants(c.Name) {
			continue
		}
		text := fmt.Sprintf("%s %d", strings.ToUpper(c.Name), c.Total)
		if c.Worse > 0 {
			text += fmt.Sprintf(" !%d", c.Worse)
		}
		parts = append(parts, text)
	}
	if d.Sleep != nil && v.wants(sleepName) {
		parts = append(parts, "SLEEP")
	}
	if len(d.Workouts) > 0 && v.wants(workoutsName) {
		parts = append(parts, "WORKOUTS")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(wrapParts(parts, " "+g.dot+" ", ti.width-2), "\n"+indent)
}

// dayCols is the column plan of the day table.
type dayCols struct {
	gutter  int
	labelW  int
	latestW int
	usualW  int
	areaW   int  // the "vs usual" cell
	textW   int  // the delta text before the band
	band    bool // the baseline band fits after the delta
	short   bool // rows use their short label
}

// maxLabelW caps the label column: the longest registry label is 26 wide.
const maxLabelW = 26

// dayLabel is the row's label: the full one, or the short one on a squeezed table.
func dayLabel(r stats.MetricRow, short bool) string {
	if short {
		return r.Short
	}
	return r.Label
}

// planDayColumns sizes the columns to the data, then squeezes them to the
// terminal: first the baseline band goes, then the latest column is cut with
// an ellipsis, then the label.
func planDayColumns(rows []stats.MetricRow, ti termInfo, g glyphSet, compact bool) dayCols {
	c := dayCols{gutter: gutterW}
	if compact {
		c.gutter = gutterW - 1
	}
	c.latestW, c.usualW, c.textW = len("LATEST"), len("USUAL"), len("VS USUAL")
	for _, r := range rows {
		c.latestW = max(c.latestW, min(runeLen(latestPlain(r, ti)), maxLatestW))
		c.usualW = max(c.usualW, runeLen(usualPlain(r)))
		c.textW = max(c.textW, runeLen(deltaPlain(r, g)))
	}
	c.textW = min(c.textW, deltaW)
	labelW := func(short bool) int {
		w := len("METRIC")
		for _, r := range rows {
			w = max(w, runeLen(dayLabel(r, short)))
		}
		return min(w, maxLabelW)
	}
	// Fixed parts: indent, gutter and the three two-space gaps.
	room := ti.width - (len(indent) + c.gutter + 2 + 2 + 2)
	withBand := c.latestW + c.usualW + c.textW + 2 + bandW

	// Prefer the full labels with the band; then short labels with the band;
	// then no band. A label is only ever cut as a last resort.
	switch {
	case labelW(false)+withBand <= room:
		c.labelW, c.band = labelW(false), true
	case labelW(true)+withBand <= room:
		c.labelW, c.band, c.short = labelW(true), true, true
	default:
		c.short = labelW(false)+c.latestW+c.usualW+c.textW > room
		c.labelW = labelW(c.short)
	}
	c.areaW = c.textW
	if c.band {
		c.areaW += 2 + bandW
	}
	// Squeeze to the terminal: the latest column loses its unit first (cut with
	// an ellipsis), then a label is cut (never below 12).
	for c.labelW+c.latestW+c.usualW+c.areaW > room && c.latestW > 6 {
		c.latestW--
	}
	c.labelW = min(c.labelW, max(room-c.latestW-c.usualW-c.areaW, 12))
	return c
}

// usualPlain is the 28-day usual as a bare number (same decimals as the
// value), "-" when there is no baseline yet.
func usualPlain(r stats.MetricRow) string {
	if r.Mean == nil {
		return "-"
	}
	return formatValue(*r.Mean, r.Metric)
}

// deltaPlain is the text of the "vs usual" cell without colour or band.
func deltaPlain(r stats.MetricRow, g glyphSet) string {
	if r.State != stats.StateOK {
		return fmt.Sprintf("need %dd (have %d)", stats.MinBaselineDays, r.BaselineDays)
	}
	text := g.deltaArrow(*r.DeltaPct) + " " + pctText(*r.DeltaPct)
	if w := verdictWord(r.Verdict); w != "" {
		text += " " + w
	}
	return text
}

// dayTable draws the header and, per category, a band and its rows.
func dayTable(d *stats.DayReport, ti termInfo, g glyphSet, st style, v overviewView) string {
	var rows []stats.MetricRow
	for _, r := range d.Metrics {
		if v.wants(r.Category) {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return ""
	}
	c := planDayColumns(rows, ti, g, v.compact)

	var b strings.Builder
	head := indent + strings.Repeat(" ", c.gutter) + padRight("METRIC", c.labelW) + "  " +
		padRight("LATEST", c.latestW) + "  " + padRight("USUAL", c.usualW) + "  VS USUAL"
	b.WriteString(st.dim(strings.TrimRight(head, " ")) + "\n")

	var current string
	for _, r := range rows {
		if r.Category != current {
			current = r.Category
			if !v.compact {
				b.WriteString("\n")
			}
			b.WriteString(categoryBand(current, summaryFor(d.Categories, current), ti, g, st))
		}
		line := rowPrefix(r, c.gutter, st)
		line += padRight(fit(dayLabel(r, c.short), c.labelW, g), c.labelW) + "  " + latestCell(r, c.latestW, ti, g, st) + "  "
		line += st.dim(padRight(usualPlain(r), c.usualW)) + "  "
		line += deltaAreaIn(r, g, st, c.band, c.areaW, false, c.textW)
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}

// dayFooter is the colour legend and the dim count of tracked metrics that
// have no reading on the day.
func dayFooter(d *stats.DayReport, ti termInfo, g glyphSet, st style) string {
	out := indent + colorLegend(st, ti.width-2) + "\n"
	if n := len(d.NoReading); n > 0 {
		text := fmt.Sprintf("%d tracked metrics have no reading on this day", n)
		if n == 1 {
			text = "1 tracked metric has no reading on this day"
		}
		out += indent + st.dim(fit(text, ti.width-2, g)) + "\n"
	}
	return out
}

// dayWorkouts is the WORKOUTS block of one day: a band with the totals, then a
// row per session (clock time, activity, duration, distance, pace, kcal) drawn
// by the same row code as the workouts section.
func dayWorkouts(ws []stats.Workout, ti termInfo, g glyphSet, st style) []string {
	total := ti.width - len(indent) - 1
	var dur time.Duration
	var kcal, km float64
	hasKcal, hasKm := false, false
	for _, w := range ws {
		dur += w.Duration
		if w.EnergyKcal != nil {
			kcal, hasKcal = kcal+*w.EnergyKcal, true
		}
		if w.DistanceM != nil && *w.DistanceM > 0 {
			km, hasKm = km+*w.DistanceM/1000, true
		}
	}
	parts := []string{plural(int64(len(ws)), "session"), formatDuration(dur)}
	if hasKcal {
		parts = append(parts, formatInt(kcal)+" kcal")
	}
	if hasKm {
		parts = append(parts, fmt.Sprintf("%.1f km", km))
	}
	// Two spaces between the parts, like the workouts section's band; parts
	// that do not fit are dropped from the end.
	summary := strings.Join(parts, "  ")
	for len(parts) > 1 && runeLen(summary) > bandRoom("WORKOUTS", total) {
		parts = parts[:len(parts)-1]
		summary = strings.Join(parts, "  ")
	}

	lines := []string{indent + bandLine("WORKOUTS", summary, total, g, st), ""}
	return append(lines, wkSessionRows(ws, "SESSIONS", 5, func(w stats.Workout) string { return w.Start.Format("15:04") }, g, st, ti.width)...)
}
