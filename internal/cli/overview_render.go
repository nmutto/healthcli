package cli

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/nmutto/healthcli/internal/stats"
)

// This file turns a stats.Overview into text. It does no I/O: everything it
// needs is in its arguments, so tests can render any report at any width.

// overviewView is what the text needs besides the report itself. It is not
// part of the report because it is not part of the --json output.
type overviewView struct {
	dbName string
	now    time.Time // for "imported 3 d ago"
}

// Fixed column widths of the metric table. The label, value and sparkline
// columns are sized from the data and the terminal width instead.
const (
	deltaW   = 8  // "▲ +17%", at most "▼ <-999%"
	bandW    = 10 // the baseline band
	ageW     = 5  // "today", "12 d"
	minSpark = 14 // a trend line shorter than two weeks is not worth drawing
	indent   = " "
)

// sectionLabelW pads SLEEP / WORKOUTS / DATA so their text lines up.
const sectionLabelW = 8

func renderOverview(o *stats.Overview, ti termInfo, v overviewView) string {
	g := glyphsFor(ti.unicode)
	st := style{color: ti.color}
	var b strings.Builder

	b.WriteString(titleLine(o, ti, v, st))
	b.WriteString(indent + strings.Repeat(string(g.rule), ti.width-2) + "\n")

	if o.Empty {
		b.WriteString("\n" + emptyBox(ti, g, st))
		return b.String()
	}

	b.WriteString(indent + headline(o, g, ti.width-2) + "\n\n")
	b.WriteString(metricTable(o, ti, g, st))
	b.WriteString("\n" + sleepBlock(o.Sleep, ti, g, st))
	b.WriteString(workoutsBlock(o.Workouts, ti, g, st))
	b.WriteString("\n" + dataBlock(o.Data, ti, g, st, v))
	return b.String()
}

// titleLine is "healthcli overview" on the left and the database and import
// age on the right, padded so the right part ends at the last column.
func titleLine(o *stats.Overview, ti termInfo, v overviewView, st style) string {
	const left = "healthcli overview"
	g := glyphsFor(ti.unicode)
	right := "db: " + v.dbName
	if li := o.Data.LastImport; li != nil {
		right += " " + g.dot + " imported " + agoText(v.now, time.Unix(li.At, 0))
	}
	right = fit(right, ti.width-len(left)-4, g)
	pad := max(ti.width-2-len(left)-runeLen(right), 1)
	return indent + st.bold(left) + strings.Repeat(" ", pad) + st.dim(right) + "\n"
}

// agoText says how long ago `then` was in whole days: "today" or "3 d ago".
func agoText(now, then time.Time) string {
	days := int(now.Sub(then).Hours() / 24)
	if days <= 0 {
		return "today"
	}
	return fmt.Sprintf("%d d ago", days)
}

// headline is the one-sentence summary at the top.
func headline(o *stats.Overview, g glyphSet, width int) string {
	compared, differ := 0, 0
	var mover *stats.MetricRow // the differing metric with the largest |z|
	waiting := false           // some metric still lacks history for a baseline
	newest := -1               // age of the freshest reading, -1 = none
	for i := range o.Metrics {
		r := &o.Metrics[i]
		switch r.State {
		case stats.StateOK:
			compared++
			if r.Differs() {
				differ++
				if mover == nil || math.Abs(*r.Z) > math.Abs(*mover.Z) {
					mover = r
				}
			}
		case stats.StateNoBaseline:
			waiting = true
		}
		if r.State != stats.StateNoData && (newest < 0 || r.AgeDays < newest) {
			newest = r.AgeDays
		}
	}

	var parts []string
	switch {
	case compared > 0:
		parts = append(parts, fmt.Sprintf("%d of %d readings differ from your %d-day usual", differ, compared, stats.DefaultBaselineDays))
	case waiting:
		parts = append(parts, fmt.Sprintf("Not enough history to compare yet (a baseline needs %d days)", stats.MinBaselineDays))
	case newest > 0:
		parts = append(parts, fmt.Sprintf("No recent readings: the newest is %d d old", newest))
	default:
		parts = append(parts, "No metric readings yet")
	}
	if mover != nil {
		parts = append(parts, fmt.Sprintf("%s %s", mover.Label, pctText(*mover.DeltaPct)))
	}
	if s := o.Sleep; s.State == stats.StateOK && s.AsleepSecs > 0 {
		parts = append(parts, "slept "+formatDuration(sec(s.AsleepSecs)))
	}
	return joinFit(parts, width, g)
}

// joinFit joins parts with the separator dot. When the result is too wide it
// drops parts from the END (the least important), and only cuts the first
// part as a last resort. That reads better than cutting a word in half.
func joinFit(parts []string, width int, g glyphSet) string {
	for n := len(parts); n > 1; n-- {
		if s := strings.Join(parts[:n], " "+g.dot+" "); runeLen(s) <= width {
			return s
		}
	}
	return fit(parts[0], width, g)
}

// pctText is a signed whole percent: "+24%", "-3%", "0%". Beyond +-999 it
// is capped, so a freak value cannot break the column.
func pctText(pct float64) string {
	r := math.Round(pct)
	switch {
	case r == 0:
		return "0%"
	case r > 999:
		return ">+999%"
	case r < -999:
		return "<-999%"
	}
	return fmt.Sprintf("%+d%%", int(r))
}

// metricTable draws the header and one row per metric.
func metricTable(o *stats.Overview, ti termInfo, g glyphSet, st style) string {
	// Plain-text cells first, so column widths can come from the data.
	labelW, latestW := len("METRIC"), len("LATEST")
	latest := make([]string, len(o.Metrics))
	for i, r := range o.Metrics {
		latest[i] = "-"
		if r.Value != nil {
			latest[i] = formatValue(*r.Value, r.Unit) + " " + unitLabel(r.Metric, r.Unit)
		}
		labelW = max(labelW, runeLen(r.Label))
		latestW = max(latestW, runeLen(latest[i]))
	}

	// Width budget. Drop the sparkline first when the line gets too long,
	// then the band. The fixed part is: indent, label, value, delta, age and
	// the two-space gaps between them.
	fixed := len(indent) + labelW + 2 + latestW + 2 + deltaW + 2 + ageW
	showBand := true
	sparkW := min(ti.width-(fixed+2+bandW)-2, stats.SparkDays)
	if sparkW < minSpark {
		sparkW = 0
		showBand = fixed+2+bandW <= ti.width
	}
	areaW := deltaW // the delta+band area, which also holds state words
	if showBand {
		areaW += 2 + bandW
	}

	var b strings.Builder
	head := indent + padRight("METRIC", labelW) + "  " + padRight("LATEST", latestW) + "  " + padRight("VS 28-DAY USUAL", areaW)
	if sparkW > 0 {
		head += "  " + padRight(fmt.Sprintf("%d DAYS", sparkW), sparkW)
	}
	head += "  AGE"
	b.WriteString(st.dim(strings.TrimRight(head, " ")) + "\n")

	for i, r := range o.Metrics {
		if r.State == stats.StateNoData {
			// Nothing to show: one muted row, built without inner colours so
			// the escape codes do not nest.
			row := indent + padRight(r.Label, labelW) + "  " + padRight(latest[i], latestW) + "  " + padRight("no data", areaW)
			if sparkW > 0 {
				row += "  " + strings.Repeat(" ", sparkW)
			}
			b.WriteString(st.dim(strings.TrimRight(row+"  -", " ")) + "\n")
			continue
		}
		area, paintArea := deltaArea(r, g, showBand, areaW)
		line := indent + padRight(r.Label, labelW) + "  " + padRight(latest[i], latestW) + "  "
		line += paintArea(st, padRight(area, areaW))
		if sparkW > 0 {
			line += "  " + padRight(sparkText(r, sparkW, g), sparkW)
		}
		line += "  " + colorIf(r.State == stats.StateStale, st.yellow, ageText(r))
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}

// colorIf applies paint to s when cond holds.
func colorIf(cond bool, paint func(string) string, s string) string {
	if cond {
		return paint(s)
	}
	return s
}

// deltaArea is the text of the "vs usual" area for a row, plus how to colour
// it. Colour never carries meaning alone: the arrow or the word says it too.
func deltaArea(r stats.MetricRow, g glyphSet, showBand bool, areaW int) (string, func(style, string) string) {
	none := func(_ style, s string) string { return s }
	dim := func(st style, s string) string { return st.dim(s) }
	switch r.State {
	case stats.StateOK:
		text := g.deltaArrow(*r.DeltaPct) + " " + pctText(*r.DeltaPct)
		text = padRight(text, deltaW)
		if showBand {
			text += "  " + g.bandBar(*r.Value, *r.Mean, *r.Stddev, bandW)
		}
		switch {
		case math.Abs(*r.Z) >= 2: // far from usual
			return text, func(st style, s string) string { return st.yellow(s) }
		case r.Differs():
			return text, func(st style, s string) string { return st.cyan(s) }
		}
		return text, none
	case stats.StateNoBaseline:
		return fit(fmt.Sprintf("baseline %d/%d days", r.BaselineDays, stats.MinBaselineDays), areaW, g), dim
	case stats.StateStale:
		return "stale", func(st style, s string) string { return st.yellow(s) }
	}
	return "no data", dim
}

// sparkText draws the last n days of the row's trend (n <= SparkDays).
func sparkText(r stats.MetricRow, n int, g glyphSet) string {
	days := r.Spark[max(len(r.Spark)-n, 0):] // the most recent n days
	vals := make([]float64, len(days))
	seen := false
	for i, p := range days {
		vals[i] = math.NaN()
		if p != nil {
			vals[i], seen = *p, true
		}
	}
	if !seen {
		return "" // a stale metric has no data in the window: no row of gap marks
	}
	return g.sparkline(vals)
}

func ageText(r stats.MetricRow) string {
	switch {
	case r.AgeDays == 0:
		return "today"
	}
	return fmt.Sprintf("%d d", r.AgeDays)
}

// sleepBlock shows the latest night: totals, a stacked bar and a legend.
func sleepBlock(s stats.OverviewSleep, ti termInfo, g glyphSet, st style) string {
	label := indent + st.bold(padRight("SLEEP", sectionLabelW)) + " "
	if s.State == stats.StateNoData {
		return label + st.dim(fmt.Sprintf("no sleep recorded in the last %d days", stats.RecentDays)) + "\n"
	}

	when := "last night"
	if s.AgeDays > 0 {
		when = fmt.Sprintf("%d d ago", s.AgeDays)
	}
	if s.State == stats.StateStale {
		when = st.yellow(when + " (stale)")
	}

	// A night with only in-bed rows has no stages: say so instead of "0m asleep".
	if s.AsleepSecs == 0 {
		return label + fmt.Sprintf("in bed %s, no sleep stages %s %s\n", formatDuration(sec(s.InBedSecs)), g.dot, when)
	}
	head := formatDuration(sec(s.AsleepSecs)) + " asleep"
	if s.InBedSecs > 0 {
		head += " " + g.dot + " " + formatDuration(sec(s.InBedSecs)) + " in bed"
	}

	stages := []struct {
		key, name string
		secs      int64
	}{
		{"core", "core", s.CoreSecs}, {"rem", "REM", s.REMSecs}, {"deep", "deep", s.DeepSecs},
		{"unspecified", "unspecified", s.UnspecSecs}, {"awake", "awake", s.AwakeSecs},
	}
	var segs []segment
	var legend []string
	for _, x := range stages {
		if x.secs > 0 {
			segs = append(segs, segment{x.key, x.secs})
			legend = append(legend, x.name+" "+formatDuration(sec(x.secs)))
		}
	}

	out := label + head + " " + g.dot + " " + when + "\n"
	if len(segs) > 0 {
		out += indent + g.stackedBar(segs, ti.width-2) + "\n"
		out += indent + fit(strings.Join(legend, "   "), ti.width-2, g) + "\n"
	}
	return out
}

func workoutsBlock(w stats.WorkoutsSummary, ti termInfo, g glyphSet, st style) string {
	label := indent + st.bold(padRight("WORKOUTS", sectionLabelW)) + " "
	if w.Count == 0 {
		return label + st.dim(fmt.Sprintf("none in the last %d days", w.Days)) + "\n"
	}
	parts := []string{
		fmt.Sprintf("%d in %d d", w.Count, w.Days),
		formatDuration(sec(w.DurationSeconds)),
	}
	if w.EnergyKcal > 0 {
		parts = append(parts, formatInt(w.EnergyKcal)+" kcal")
	}
	if l := w.Latest; l != nil {
		text := "latest: " + l.Activity
		if l.DistanceM != nil {
			text += fmt.Sprintf(" %.1f km", *l.DistanceM/1000)
		}
		if l.AgeDays == 0 {
			text += ", today"
		} else {
			text += fmt.Sprintf(", %d d ago", l.AgeDays)
		}
		parts = append(parts, text)
	}
	return label + joinFit(parts, ti.width-2-sectionLabelW-1, g) + "\n"
}

// dataBlock is the "how complete is this data" footer.
func dataBlock(d stats.DataHealth, ti termInfo, g glyphSet, st style, v overviewView) string {
	label := indent + st.bold(padRight("DATA", sectionLabelW)) + " "
	cont := strings.Repeat(" ", len(indent)+sectionLabelW+1) // lines after the first
	room := ti.width - len(cont) - 1
	sep := " " + g.dot + " "

	lines := []string{formatInt(float64(d.Samples)) + " samples"}
	if d.Samples > 0 {
		lines[0] += sep + d.FirstDay + " " + g.arrow + " " + d.LastDay
		lines = append(lines, "sources: "+sourcesText(d.SourceCount))
	}
	if li := d.LastImport; li != nil {
		lines = append(lines, importLines(li, g, room)...)
	}
	if len(d.Gaps) > 0 {
		lines = append(lines, gapsText(d.Gaps))
	}

	var b strings.Builder
	for i, l := range lines {
		prefix := cont
		if i == 0 {
			prefix = label
		}
		b.WriteString(prefix + fit(l, room, g) + "\n")
	}
	return b.String()
}

// sourcesText is "Watch 79%, iPhone 20%, manual 1%". The percentages come from
// largestRemainder so they always add up to exactly 100.
func sourcesText(counts map[string]int64) string {
	classes := []struct{ key, name string }{
		{stats.ClassWatch, "Watch"}, {stats.ClassIPhone, "iPhone"},
		{stats.ClassManual, "manual"}, {stats.ClassOther, "other"},
	}
	var sizes []int64
	var names []string
	for _, c := range classes {
		if n := counts[c.key]; n > 0 {
			sizes, names = append(sizes, n), append(names, c.name)
		}
	}
	var parts []string
	for i, pct := range largestRemainder(sizes, 100) {
		text := fmt.Sprintf("%d%%", pct)
		if pct == 0 {
			text = "<1%" // present, but too small to earn a whole percent
		}
		parts = append(parts, names[i]+" "+text)
	}
	return strings.Join(parts, ", ")
}

// importLines describes the last import on one or two lines:
//
//	last import: 1,230 added · 40 duplicates
//	skipped: 5 (unknown_type 3, bad_date 1, …)
func importLines(li *stats.ImportInfo, g glyphSet, room int) []string {
	lines := []string{fmt.Sprintf("last import: %s added %s %s duplicates",
		formatInt(float64(li.Inserted)), g.dot, formatInt(float64(li.Duplicates)))}

	var skipped int64
	type reason struct {
		name string
		n    int64
	}
	var reasons []reason
	for name, n := range li.Skipped {
		skipped += n
		reasons = append(reasons, reason{name, n})
	}
	if skipped == 0 {
		return lines
	}
	// Map order is random, so sort: biggest first, then by name.
	sort.Slice(reasons, func(i, j int) bool {
		if reasons[i].n != reasons[j].n {
			return reasons[i].n > reasons[j].n
		}
		return reasons[i].name < reasons[j].name
	})
	// Show as many reasons as fit on the line, biggest first; "…" marks the
	// ones left out (all of them are in `import --json`).
	all := make([]string, len(reasons))
	for i, r := range reasons {
		all[i] = fmt.Sprintf("%s %d", r.name, r.n)
	}
	prefix := "skipped: " + formatInt(float64(skipped)) + " ("
	line := ""
	for n := len(all); n >= 0; n-- {
		shown := all[:n]
		if n < len(all) {
			shown = append(append([]string(nil), shown...), g.ellipsis)
		}
		line = prefix + strings.Join(shown, ", ") + ")"
		if runeLen(line) <= room {
			break
		}
	}
	return append(lines, line)
}

// gapsText lists up to three metrics with missing days, worst first.
func gapsText(gaps []stats.Gap) string {
	sorted := append([]stats.Gap(nil), gaps...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Missing > sorted[j].Missing })
	var parts []string
	for _, x := range sorted[:min(len(sorted), 3)] {
		parts = append(parts, fmt.Sprintf("%s %d of %d", x.Label, x.Missing, x.Span))
	}
	return "days missing: " + strings.Join(parts, ", ")
}

// emptyBox is the onboarding message for a database with nothing in it.
func emptyBox(ti termInfo, g glyphSet, st style) string {
	lines := []string{
		"No data yet. Two ways to start:",
		"  import:  healthcli import apple <export.zip>",
		"  log:     healthcli log weight 72.5",
	}
	inner := 0
	for _, l := range lines {
		inner = max(inner, runeLen(l))
	}
	h := string(g.box[4])
	var b strings.Builder
	b.WriteString(indent + string(g.box[0]) + strings.Repeat(h, inner+2) + string(g.box[1]) + "\n")
	for _, l := range lines {
		b.WriteString(indent + string(g.box[5]) + " " + padRight(l, inner) + " " + string(g.box[5]) + "\n")
	}
	b.WriteString(indent + string(g.box[2]) + strings.Repeat(h, inner+2) + string(g.box[3]) + "\n")
	return b.String()
}

func runeLen(s string) int { return len([]rune(s)) }

// fit shortens s to at most width characters, ending with an ellipsis.
func fit(s string, width int, g glyphSet) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	e := []rune(g.ellipsis)
	if width <= len(e) {
		return string(r[:max(width, 0)])
	}
	return string(r[:width-len(e)]) + g.ellipsis
}
