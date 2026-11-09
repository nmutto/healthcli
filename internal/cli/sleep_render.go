package cli

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nmutto/healthcli/internal/stats"
)

// This file draws the SLEEP section from a stats.SleepReport. It is pure:
// report, terminal and width in, lines out. "overview" uses the compact
// mode, "healthcli sleep" the full one.

// sleepOpts selects what renderSleepSection draws.
type sleepOpts struct {
	Full     bool // all stat rows and the heatmap; otherwise hypnogram + core rows
	NoHeader bool // skip the "SLEEP ..." title and rule (the caller draws its own)
	// NoHeatmap leaves the calendar out of the full section (the caller draws it).
	NoHeatmap bool
	Target    time.Duration
}

// sleepIndent is the left margin of the section.
const sleepIndent = " "

// minHypnogramCells is the narrowest hypnogram worth drawing; below it the
// stacked bar is used.
const minHypnogramCells = 20

// narrowSleepWidth: below this only the bar and three headline numbers fit.
const narrowSleepWidth = 50

// renderSleepSection returns the lines of the SLEEP section, or nil when
// there is no sleep data at all (the section then simply does not exist).
func renderSleepSection(r *stats.SleepReport, ti termInfo, width int, opts sleepOpts) []string {
	if r == nil || r.Empty {
		return nil
	}
	g := glyphsFor(ti.unicode)
	st := style{color: ti.color}
	last := r.Last
	var lines []string

	if !opts.NoHeader {
		lines = append(lines, sleepHeader(r, g, width, st), sleepIndent+strings.Repeat(string(g.rule), width-2))
	}

	// In bed only: no sleep, so no stages, stats or heatmap.
	if r.Kind == "in_bed" {
		lines = append(lines, sleepIndent+"in bed "+formatDuration(last.InBed)+", no sleep stages")
		if last.InBedStart > 0 {
			in, out := localClock(last.InBedStart, last.TZ), localClock(last.InBedEnd, last.TZ)
			lines = append(lines, sleepIndent+"in bed "+in+" "+g.arrow+" "+out)
		}
		return lines
	}

	target := opts.Target
	if target == 0 {
		target = stats.DefaultSleepTarget
	}

	if width < narrowSleepWidth {
		return append(lines, narrowSleep(r, g, width, target)...)
	}

	// The picture: hypnogram, or a single bar when there are no stages (or no room).
	if cells := width - 8; r.Kind == "stages" && cells >= minHypnogramCells && len(last.Segments) > 0 {
		lines = append(lines, hypnogram(last, cells, g, st)...)
	} else {
		lines = append(lines, sleepBar(r, width, g, st)...)
	}

	lines = append(lines, "")
	lines = append(lines, sleepStatTable(r, ti, width, opts, target)...)

	if opts.Full && !opts.NoHeatmap {
		if cal := sleepCalendar(r, ti, width); len(cal) > 0 {
			lines = append(lines, "")
			lines = append(lines, cal...)
		}
	}
	return lines
}

// sleepHeader is "SLEEP  Mon 9 Nov · Watch stages, iPhone in bed".
func sleepHeader(r *stats.SleepReport, g glyphSet, width int, st style) string {
	t, _ := time.Parse(stats.DayLayout, r.Last.Night)
	day := t.Format("Mon 2 Jan")
	if r.AgeDays > 0 {
		day += " (" + plural(int64(r.AgeDays), "day") + " ago)"
	}
	rest := fit("  "+day+" "+g.dot+" "+sleepSources(r.Last, r.Kind), width-len(sleepIndent)-len("SLEEP"), g)
	return sleepIndent + st.bold("SLEEP") + rest
}

// sleepSources says where the numbers came from: "Watch stages, iPhone in bed".
func sleepSources(n stats.SleepNight, kind string) string {
	label := sourceLabel(n.Source)
	var s string
	switch kind {
	case "in_bed":
		return sourceLabel(n.InBedSource) + " in bed, no sleep recorded"
	case "unspecified":
		s = label + ", no stages (unspecified asleep only)"
	default:
		s = label + " stages"
	}
	if n.InBedSource != "" {
		if n.InBedSource == n.Source {
			s += ", in bed"
		} else {
			s += ", " + sourceLabel(n.InBedSource) + " in bed"
		}
	}
	return s
}

// sourceLabel shortens a source name to Watch or iPhone when it is one.
func sourceLabel(source string) string {
	switch stats.SourceClass(source) {
	case stats.ClassWatch:
		return "Watch"
	case stats.ClassIPhone:
		return "iPhone"
	}
	if source == "" {
		return "unknown source"
	}
	return source
}

// localClock is "23:12" for a Unix time on the clock it was recorded on.
func localClock(ts int64, tz int) string {
	return time.Unix(ts, 0).In(time.FixedZone("", tz)).Format("15:04")
}

// ---- the picture -----------------------------------------------------------

// hypnogramRows are the four rows, top to bottom like a clinical chart.
var hypnogramRows = [4]struct{ label, stage string }{
	{"awake ", "awake"}, {"REM   ", "rem"}, {"core  ", "core"}, {"deep  ", "deep"},
}

// hypnogram draws one night as four rows of `cells` columns plus a time
// ruler. A column covers an equal slice of the night and shows the stage
// that fills most of it, except that awake wins any column it touches:
// otherwise a 6-minute wake-up would vanish at 8 minutes per column.
func hypnogram(n stats.SleepNight, cells int, g glyphSet, st style) []string {
	from, to := n.Start, n.End
	span := to - from
	if span <= 0 || cells < 1 {
		return nil
	}
	var grid [4][]rune
	for i := range grid {
		grid[i] = []rune(strings.Repeat(" ", cells))
	}
	for c := 0; c < cells; c++ {
		lo := from + span*int64(c)/int64(cells)
		hi := from + span*int64(c+1)/int64(cells)
		var cover [4]int64 // seconds per row: awake, rem, core, deep
		for _, s := range n.Segments {
			overlap := min(s.End, hi) - max(s.Start, lo)
			if overlap <= 0 {
				continue
			}
			switch s.Stage {
			case "awake":
				cover[0] += overlap
			case "rem":
				cover[1] += overlap
			case "deep":
				cover[3] += overlap
			default: // core and unspecified share the middle row
				cover[2] += overlap
			}
		}
		row := -1
		switch {
		case cover[0] > 0:
			row = 0
		default:
			var best int64
			for i := 1; i < 4; i++ {
				if cover[i] > best {
					best, row = cover[i], i
				}
			}
		}
		if row >= 0 {
			grid[row][c] = g.stages[hypnogramRows[row].stage]
		}
	}

	// Time ruler: first and last clock on top, hour ticks below.
	startL, endL := localClock(from, n.TZ), localClock(to, n.TZ)
	top := sleepIndent + strings.Repeat(" ", 6) + startL +
		strings.Repeat(" ", max(cells-len(startL)-len(endL), 1)) + endL
	lines := []string{st.dim(top)}
	for i, row := range hypnogramRows {
		cellsText := string(grid[i])
		if i == 0 {
			cellsText = st.yellow(cellsText)
		} else {
			cellsText = st.cyan(cellsText)
		}
		lines = append(lines, sleepIndent+row.label+cellsText)
	}
	lines = append(lines, st.dim(sleepIndent+strings.Repeat(" ", 6)+hourTicks(from, to, n.TZ, cells)))
	return lines
}

// hourTicks writes the hour number under the column where each full hour
// starts, skipping a label that would touch the previous one.
func hourTicks(from, to int64, tz, cells int) string {
	line := []rune(strings.Repeat(" ", cells))
	end := 0 // first free column
	// The first full hour at or after `from` on the local clock.
	first := from + 3600 - (from+int64(tz))%3600
	if (from+int64(tz))%3600 == 0 {
		first = from
	}
	for t := first; t < to; t += 3600 {
		col := int((t - from) * int64(cells) / (to - from))
		label := []rune(fmt.Sprint(time.Unix(t, 0).In(time.FixedZone("", tz)).Hour()))
		if col < end || col+len(label) > cells {
			continue
		}
		copy(line[col:], label)
		end = col + len(label) + 1
	}
	return strings.TrimRight(string(line), " ")
}

// sleepBar is the one-line fallback: stage proportions as a stacked bar
// (or a single bar when only "asleep" is known), with a legend line.
func sleepBar(r *stats.SleepReport, width int, g glyphSet, st style) []string {
	n := r.Last
	cells := width - 2
	if r.Kind == "unspecified" {
		bar := g.stackedBar([]segment{{"unspecified", 1}}, cells)
		info := fmt.Sprintf("asleep %s   %s %s %s   midpoint %s", formatDuration(n.Asleep),
			localClock(n.Start, n.TZ), g.arrow, localClock(n.End, n.TZ), localClock(n.Start+(n.End-n.Start)/2, n.TZ))
		note := "(stages not recorded)"
		if pad := cells - runeLen(info) - runeLen(note); pad >= 2 {
			info += strings.Repeat(" ", pad) + st.dim(note)
		} else {
			info += "  " + st.dim(note)
		}
		return []string{sleepIndent + st.cyan(bar), sleepIndent + fit(info, cells, g)}
	}
	segs := []segment{
		{"awake", int64(n.Awake)}, {"rem", int64(n.REM)}, {"core", int64(n.Core)}, {"deep", int64(n.Deep)},
	}
	legend := fmt.Sprintf("%c awake %s  %c REM %s  %c core %s  %c deep %s",
		g.stages["awake"], formatDuration(n.Awake), g.stages["rem"], formatDuration(n.REM),
		g.stages["core"], formatDuration(n.Core), g.stages["deep"], formatDuration(n.Deep))
	return []string{sleepIndent + st.cyan(g.stackedBar(segs, cells)), sleepIndent + st.dim(fit(legend, cells, g))}
}

// narrowSleep is the whole section on a very narrow terminal: a bar and
// three headline numbers.
func narrowSleep(r *stats.SleepReport, g glyphSet, width int, target time.Duration) []string {
	n := r.Last
	st := style{}
	lines := sleepBar(r, width, g, st)[:1]
	parts := []string{"asleep " + formatDuration(n.Asleep)}
	if eff, _, ok := n.Efficiency(); ok {
		parts = append(parts, fmt.Sprintf("eff %.0f%%", eff))
	}
	parts = append(parts, debtWords(int64((target-n.Asleep)/time.Second)))
	return append(lines, fit(sleepIndent+strings.Join(parts, "  "), width, g))
}

// ---- the stat table ---------------------------------------------------------

// statKind says how a row's numbers are compared with the usual.
type statKind int

const (
	kindDuration statKind = iota // seconds; change as a percentage
	kindPercent                  // efficiency; change in points
	kindClock                    // minutes since 18:00; change in minutes
)

// sleepStat describes one row of the table. The polarity table is local to
// sleep: sleep metrics are not rows of the metrics registry.
type sleepStat struct {
	name     string
	compact  bool // shown in the overview
	kind     statKind
	polarity int     // +1 higher is better, -1 lower is better, 0 no good direction
	minDelta float64 // smaller changes are "typical": percent, points or minutes
	// last formats last night's value ("" = missing), week its average.
	last func(n stats.SleepNight) string
	val  func(a *stats.SleepAgg) *float64
	// weekText overrides the average column text (default: from val).
	weekText func(a *stats.SleepAgg) string
}

func secsText(s float64) string { return formatDuration(time.Duration(s * float64(time.Second))) }

// stagePct is " 21%": the stage's share of the night's sleep.
func stagePct(part, total time.Duration) string {
	if total <= 0 {
		return ""
	}
	return fmt.Sprintf(" %.0f%%", float64(part)/float64(total)*100)
}

func clockCell(f func(stats.SleepNight) (float64, bool)) func(stats.SleepNight) string {
	return func(n stats.SleepNight) string {
		if v, ok := f(n); ok {
			return stats.ClockText(v)
		}
		return ""
	}
}

var sleepStats = []sleepStat{
	{name: "Asleep", compact: true, kind: kindDuration, polarity: +1, minDelta: 5,
		last: func(n stats.SleepNight) string { return ifThen(n.Asleep > 0, formatDuration(n.Asleep)) },
		val:  func(a *stats.SleepAgg) *float64 { return a.AsleepSecs }},
	{name: "In bed", compact: true, kind: kindDuration, polarity: 0, minDelta: 5,
		last: func(n stats.SleepNight) string { return ifThen(n.InBed > 0, formatDuration(n.InBed)) },
		val:  func(a *stats.SleepAgg) *float64 { return a.InBedSecs }},
	{name: "Efficiency", compact: true, kind: kindPercent, polarity: +1, minDelta: 2,
		last: func(n stats.SleepNight) string {
			if p, _, ok := n.Efficiency(); ok {
				return fmt.Sprintf("%.0f%%", p)
			}
			return ""
		},
		val: func(a *stats.SleepAgg) *float64 { return a.Efficiency }},
	{name: "Awake", compact: true, kind: kindDuration, polarity: -1, minDelta: 10,
		last: func(n stats.SleepNight) string {
			return ifThen(n.HasStages(), formatDuration(n.Awake)+fmt.Sprintf(" %dx", n.Awakenings))
		},
		val: func(a *stats.SleepAgg) *float64 { return a.AwakeSecs },
		weekText: func(a *stats.SleepAgg) string {
			if a.AwakeSecs == nil || a.Awakenings == nil {
				return ""
			}
			return secsText(*a.AwakeSecs) + fmt.Sprintf(" %.0fx", *a.Awakenings)
		}},
	{name: "Latency", kind: kindDuration, polarity: -1, minDelta: 10,
		last: func(n stats.SleepNight) string { return ifThen(n.Latency > 0, "~"+formatDuration(n.Latency)) },
		val:  func(a *stats.SleepAgg) *float64 { return a.LatencySecs }},
	{name: "Deep", compact: true, kind: kindDuration, polarity: +1, minDelta: 5,
		last: func(n stats.SleepNight) string {
			return ifThen(n.HasStages(), formatDuration(n.Deep)+stagePct(n.Deep, n.Asleep))
		},
		val: func(a *stats.SleepAgg) *float64 { return a.DeepSecs }},
	{name: "REM", compact: true, kind: kindDuration, polarity: +1, minDelta: 5,
		last: func(n stats.SleepNight) string {
			return ifThen(n.HasStages(), formatDuration(n.REM)+stagePct(n.REM, n.Asleep))
		},
		val: func(a *stats.SleepAgg) *float64 { return a.REMSecs }},
	{name: "Core", kind: kindDuration, polarity: 0, minDelta: 5,
		last: func(n stats.SleepNight) string {
			return ifThen(n.HasStages(), formatDuration(n.Core)+stagePct(n.Core, n.Asleep))
		},
		val: func(a *stats.SleepAgg) *float64 { return a.CoreSecs }},
	{name: "Bedtime", compact: true, kind: kindClock, minDelta: 15,
		last: clockCell(stats.SleepNight.BedtimeMin), val: func(a *stats.SleepAgg) *float64 { return a.BedtimeMin }},
	{name: "Wake", kind: kindClock, minDelta: 15,
		last: clockCell(stats.SleepNight.WakeMin), val: func(a *stats.SleepAgg) *float64 { return a.WakeMin }},
	{name: "Midpoint", compact: true, kind: kindClock, minDelta: 15,
		last: clockCell(stats.SleepNight.MidpointMin), val: func(a *stats.SleepAgg) *float64 { return a.MidpointMin }},
}

func ifThen(ok bool, s string) string {
	if ok {
		return s
	}
	return ""
}

// sleepVerdict is the comparison of a week average with the usual: the
// arrow, the text and the word that also carries the colour.
type sleepVerdict struct {
	text    string // "▼ -5% worse"
	verdict string // better | worse | changed | typical
}

// compareSleep sets week against usual for one row. ok is false when either
// number is missing.
func compareSleep(s sleepStat, week, usual *float64, g glyphSet) (sleepVerdict, bool) {
	if week == nil || usual == nil {
		return sleepVerdict{}, false
	}
	var delta, shown float64 // delta in the unit of minDelta; shown is what is printed
	var unit string
	switch s.kind {
	case kindDuration:
		if *usual == 0 {
			return sleepVerdict{}, false
		}
		delta = (*week - *usual) / *usual * 100
		unit = "%"
	case kindPercent:
		delta, unit = *week-*usual, " pts"
	default:
		delta, unit = *week-*usual, "m"
	}
	shown = delta
	if math.Abs(delta) < s.minDelta {
		return sleepVerdict{text: g.flat + " typical", verdict: "typical"}, true
	}
	arrow := g.up
	if delta < 0 {
		arrow = g.down
	}
	// Direction words. Clock rows have no good direction: later / earlier.
	// Too long (over 9.5 h) or too quick falling asleep (under 5 min) is not "good".
	polarity := s.polarity
	switch {
	case s.name == "Asleep" && (*week > 9.5*3600 || *usual > 9.5*3600):
		polarity = 0
	case s.name == "Latency" && *week < 5*60:
		polarity = 0
	}
	word, verdict := "changed", "changed"
	switch {
	case s.kind == kindClock:
		word = map[bool]string{true: "later", false: "earlier"}[delta > 0]
	case polarity != 0 && (delta > 0) == (polarity > 0):
		word, verdict = "better", "better"
	case polarity != 0:
		word, verdict = "worse", "worse"
	}
	return sleepVerdict{text: fmt.Sprintf("%s %+.0f%s %s", arrow, shown, unit, word), verdict: verdict}, true
}

// colourVerdict colours a verdict: green better, red worse, cyan changed or
// neutral, plain when typical. The words carry the meaning without colour.
func (st style) verdict(v, s string) string {
	switch v {
	case "better":
		return st.green(s)
	case "worse":
		return st.red(s)
	case "changed", "later", "earlier":
		return st.cyan(s)
	}
	return st.dim(s)
}

// sleepStatTable draws the STAT / LAST NIGHT / n NIGHTS / VS USUAL table
// plus the regularity and debt lines. Rows with no data are not drawn; one
// dim line names them.
func sleepStatTable(r *stats.SleepReport, ti termInfo, width int, opts sleepOpts, target time.Duration) []string {
	g := glyphsFor(ti.unicode)
	st := style{color: ti.color}
	last := r.Last

	nameW, lastW, weekW := 11, 12, 11
	if width >= 80 {
		nameW, lastW, weekW = 13, 16, 14
	}
	needed := stats.MinSleepNights
	haveUsual := r.Usual != nil && r.Usual.Nights >= needed

	weekHead := fmt.Sprintf("%d NIGHTS", r.Week.Nights)
	if r.Week.Nights == 1 {
		weekHead = "1 NIGHT"
	}
	usualHead := "VS USUAL"
	if width >= 60 && haveUsual {
		usualHead = fmt.Sprintf("VS %d-NIGHT USUAL", r.UsualNights)
	}
	lines := []string{st.dim(sleepIndent + padRight("STAT", nameW) + padRight("LAST NIGHT", lastW) + padRight(weekHead, weekW) + usualHead)}

	var missing []string
	for _, s := range sleepStats {
		if !opts.Full && !s.compact {
			continue
		}
		lastText := s.last(last)
		weekText := ""
		if s.weekText != nil {
			weekText = s.weekText(r.Week)
		} else if v := s.val(r.Week); v != nil {
			weekText = formatSleepValue(s, *v)
		}
		if lastText == "" && weekText == "" {
			missing = append(missing, strings.ToLower(s.name))
			continue
		}
		var vs string
		if !haveUsual {
			if lastText != "" || weekText != "" {
				vs = st.dim(fmt.Sprintf("need %d nights (have %d)", needed, r.Usual.Nights))
			}
		} else if v, ok := compareSleep(s, s.val(r.Week), s.val(r.Usual), g); ok {
			vs = st.verdict(v.verdict, v.text)
		}
		lines = append(lines, sleepIndent+padRight(s.name, nameW)+padRight(lastText, lastW)+padRight(weekText, weekW)+vs)
	}

	if opts.Full {
		lines = append(lines, regularityLine(r, g, st, width, nameW)...)
	}
	if line := debtLine(r, target, nameW, width); line != "" {
		lines = append(lines, line)
	}
	if len(missing) > 0 {
		lines = append(lines, st.dim(sleepIndent+"not available: "+strings.ReplaceAll(strings.Join(missing, ", "), "rem", "REM")))
	}
	if _, clamped, _ := last.Efficiency(); clamped {
		lines = append(lines, st.dim(sleepIndent+"efficiency capped at 100%: sleep and in-bed sources differ"))
	}
	if opts.Full && haveUsual {
		lines = append(lines, st.dim(sleepIndent+"green better / red worse / cyan neutral"))
	}
	return lines
}

// formatSleepValue writes an average in the row's own unit.
func formatSleepValue(s sleepStat, v float64) string {
	switch s.kind {
	case kindPercent:
		return fmt.Sprintf("%.0f%%", v)
	case kindClock:
		return stats.ClockText(v)
	}
	return secsText(v)
}

// regularityLine is "Regularity ±38m bed · ±31m mid (14 n)  ▼ better than usual ±52m".
func regularityLine(r *stats.SleepReport, g glyphSet, st style, width, nameW int) []string {
	reg := r.Regularity
	if reg == nil {
		return nil
	}
	pm := map[bool]string{true: "±", false: "+/-"}[g.dot == "·"]
	line := fmt.Sprintf("%s%s%s%.0fm bed %s%.0fm mid", sleepIndent, padRight("Regularity", nameW), pm, reg.BedtimeMin, pm, reg.MidpointMin)
	if width >= 70 {
		line += fmt.Sprintf(" (%d n)", reg.Nights)
	}
	if reg.UsualBedtimeMin > 0 {
		diff := reg.BedtimeMin - reg.UsualBedtimeMin
		word, verdict, arrow := "same", "typical", g.flat
		switch {
		case diff <= -5:
			word, verdict, arrow = "better", "better", g.down
		case diff >= 5:
			word, verdict, arrow = "worse", "worse", g.up
		}
		usual := fmt.Sprintf("%s %s than usual %.0fm", arrow, word, reg.UsualBedtimeMin)
		if width < 70 {
			usual = fmt.Sprintf("%s %s (usual %.0fm)", arrow, word, reg.UsualBedtimeMin)
		}
		line += "   " + st.verdict(verdict, usual)
	}
	return []string{line}
}

// debtLine compares sleep with the target: "59m short last night, 7 n: 4h 20m short vs 8h target".
func debtLine(r *stats.SleepReport, target time.Duration, nameW, width int) string {
	d := r.Debt
	if d == nil {
		return ""
	}
	text := debtWords(d.LastSecs)
	if width >= 70 {
		text += " last night"
	}
	if d.Nights > 1 {
		text += fmt.Sprintf(", %d n: %s", d.Nights, debtWords(d.WindowSecs))
	}
	tgt := formatDuration(target)
	tgt = strings.Replace(tgt, " 00m", "", 1)
	if width >= 70 {
		text += " vs " + tgt + " target"
	} else {
		text += " (" + tgt + " target)"
	}
	return sleepIndent + padRight("Sleep debt", nameW) + text
}

// debtWords turns seconds short of the target (negative: surplus) into
// "59m short" or "20m over".
func debtWords(secs int64) string {
	d := time.Duration(secs) * time.Second
	switch {
	case d > 0:
		return formatDuration(d) + " short"
	case d < 0:
		return formatDuration(-d) + " over"
	}
	return "on target"
}

// ---- the calendar -----------------------------------------------------------

// sleepCalendar is the heatmap when there are enough nights, else one small
// bar per night.
func sleepCalendar(r *stats.SleepReport, ti termInfo, width int) []string {
	withSleep := 0
	for _, n := range r.Nights {
		if n.Asleep > 0 {
			withSleep++
		}
	}
	if withSleep == 0 {
		return nil
	}
	if r.Heatmap != nil && withSleep >= stats.MinHeatmapNights {
		ti.width = width - len(sleepIndent)
		return indentLines(renderHeatmap(r.Heatmap, ti))
	}
	if r.Heatmap == nil { // --weeks 0 hides it
		return nil
	}
	return indentLines(nightBars(r, ti, width-len(sleepIndent)))
}

func indentLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if l != "" {
			l = sleepIndent + l
		}
		out[i] = l
	}
	return out
}

// nightBars is the fallback under 14 nights: a column per night with the
// weekday, hours asleep and a bar scaled to 10 hours.
func nightBars(r *stats.SleepReport, ti termInfo, width int) []string {
	g := glyphsFor(ti.unicode)
	st := style{color: ti.color}
	var nights []stats.SleepNight
	for _, n := range r.Nights {
		if n.Asleep > 0 {
			nights = append(nights, n)
		}
	}
	perRow := max((width-2)/4, 1)
	nights = nights[max(len(nights)-min(perRow, 14), 0):]
	lines := []string{
		fmt.Sprintf("time asleep per night (a calendar needs %d nights, have %d)", stats.MinHeatmapNights, len(nights)),
	}
	var days, hours, bars strings.Builder
	for _, n := range nights {
		t, _ := time.Parse(stats.DayLayout, n.Night)
		days.WriteString(padRight(t.Format("Mon"), 4))
		hours.WriteString(padRight(fmt.Sprintf("%.0fh", n.Asleep.Hours()), 4))
		level := min(int(math.Round(n.Asleep.Hours()/10*float64(len(g.bars)-1))), len(g.bars)-1)
		bars.WriteString(padRight(strings.Repeat(string(g.bars[max(level, 0)]), 3), 4))
	}
	lines = append(lines, strings.TrimRight(days.String(), " "), strings.TrimRight(hours.String(), " "), st.cyan(strings.TrimRight(bars.String(), " ")))
	return lines
}
