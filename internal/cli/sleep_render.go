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
		lines = append(lines, sleepHeader(r, g, width, st))
	}

	// In bed only: no sleep, so no stages, stats or heatmap.
	if r.Kind == "in_bed" {
		lines = append(lines, sleepIndent+"in bed "+formatDuration(last.InBed)+", no sleep stages")
		if last.InBedStart > 0 {
			in, out := localClock(last.InBedStart, last.InBedStartTZ), localClock(last.InBedEnd, last.InBedEndTZ)
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

// sleepHeader is the section band: "━━ SLEEP ━━ Mon 9 Nov · Watch stages, iPhone in bed ━━".
// The subtitle is never cut with an ellipsis: when it does not fit, the
// "(N days ago)" goes first, then the long source text, then the sources.
func sleepHeader(r *stats.SleepReport, g glyphSet, width int, st style) string {
	t, _ := time.Parse(stats.DayLayout, r.Last.Night)
	day := t.Format("Mon 2 Jan")
	ago := ""
	if r.AgeDays > 0 {
		ago = " (" + plural(int64(r.AgeDays), "day") + " ago)"
	}
	total := width - len(sleepIndent) - 1
	room := bandRoom("SLEEP", total)
	short := sleepSourcesShort(r.Last, r.Kind)
	var subtitle string
	for _, cand := range []string{
		day + ago + " " + g.dot + " " + sleepSources(r.Last, r.Kind),
		day + " " + g.dot + " " + sleepSources(r.Last, r.Kind),
		day + " " + g.dot + " " + short,
		day,
	} {
		subtitle = cand
		if runeLen(cand) <= room {
			break
		}
	}
	return sleepIndent + bandLine("SLEEP", subtitle, total, g, st)
}

// sleepSourcesShort is the source text in few characters: "Watch, iPhone".
func sleepSourcesShort(n stats.SleepNight, kind string) string {
	switch {
	case kind == "in_bed":
		return sourceLabel(n.InBedSource, n.InBedClass) + " in bed"
	case n.InBedSource != "" && n.InBedSource != n.Source:
		return sourceLabel(n.Source, n.SourceClass) + ", " + sourceLabel(n.InBedSource, n.InBedClass)
	}
	return sourceLabel(n.Source, n.SourceClass)
}

// sleepSources says where the numbers came from: "Watch stages, iPhone in bed".
func sleepSources(n stats.SleepNight, kind string) string {
	label := sourceLabel(n.Source, n.SourceClass)
	var s string
	switch kind {
	case "in_bed":
		return sourceLabel(n.InBedSource, n.InBedClass) + " in bed, no sleep recorded"
	case "unspecified":
		s = label + ", no stages (unspecified asleep only)"
	default:
		s = label + " stages"
	}
	if n.InBedSource != "" {
		if n.InBedSource == n.Source {
			s += ", in bed"
		} else {
			s += ", " + sourceLabel(n.InBedSource, n.InBedClass) + " in bed"
		}
	}
	return s
}

// sourceLabel shortens a source to Watch or iPhone when its class says so.
func sourceLabel(source, class string) string {
	if class == "" { // a night built without a class (tests): go by the name
		class = stats.SourceClass(source, "")
	}
	switch class {
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
// otherwise a 6-minute wake-up would vanish at 8 minutes per column. Two
// separate awakenings keep a blank column between them when one of them is
// wide enough to give one up, so they do not read as a single block.
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
	awakeCol := awakeColumns(n, cells)
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
		case awakeCol[c]:
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
	startL, endL := localClock(from, n.StartTZ), localClock(to, n.EndTZ)
	top := sleepIndent + strings.Repeat(" ", 6) + startL +
		strings.Repeat(" ", max(cells-len(startL)-len(endL), 1)) + endL
	lines := []string{st.dim(top)}
	for i, row := range hypnogramRows {
		lines = append(lines, sleepIndent+row.label+st.stage(row.stage, string(grid[i])))
	}
	lines = append(lines, st.dim(sleepIndent+strings.Repeat(" ", 6)+hourTicks(from, to, n.StartTZ, cells)))
	return lines
}

// awakeColumns marks the columns that show awake. Each awake period (rows
// that touch count as one) gets the columns it touches; when one starts in
// the column right after the previous one ends, the wider of the two gives up
// an edge column so a blank column separates them. Two single-column
// awakenings next to each other cannot be told apart and stay joined.
func awakeColumns(n stats.SleepNight, cells int) []bool {
	span := n.End - n.Start
	col := func(ts int64) int { return int(min(max((ts-n.Start)*int64(cells)/span, 0), int64(cells-1))) }
	var spans [][2]int // first and last column of each awake period
	var lastEnd int64
	for _, s := range n.Segments {
		if s.Stage != "awake" || s.End <= n.Start || s.Start >= n.End {
			continue
		}
		c0, c1 := col(s.Start), col(s.End-1)
		if k := len(spans); k > 0 && s.Start <= lastEnd {
			spans[k-1][1] = max(spans[k-1][1], c1)
		} else {
			spans = append(spans, [2]int{c0, c1})
		}
		lastEnd = max(lastEnd, s.End)
	}
	for i := 1; i < len(spans); i++ {
		prev, cur := &spans[i-1], &spans[i]
		for cur[0] <= prev[1]+1 {
			switch {
			case prev[1] > prev[0]:
				prev[1]--
			case cur[1] > cur[0]:
				cur[0]++
			default:
				break
			}
			if prev[1] == prev[0] && cur[1] == cur[0] {
				break
			}
		}
	}
	marked := make([]bool, cells)
	for _, sp := range spans {
		for c := sp[0]; c <= sp[1]; c++ {
			marked[c] = true
		}
	}
	return marked
}

// hourTicks writes the clock hour (00 01 02 ...) under the column where each full hour
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
		label := []rune(fmt.Sprintf("%02d", time.Unix(t, 0).In(time.FixedZone("", tz)).Hour()))
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
		bar := g.stackedBarPainted([]segment{{"unspecified", 1}}, cells, st.stage)
		mid, _ := n.MidpointMin() // the same number as the table's Midpoint row
		span := localClock(n.Start, n.StartTZ) + " " + g.arrow + " " + localClock(n.End, n.EndTZ)
		asleep := "asleep " + formatDuration(n.Asleep)
		// The longest wording that fits: all the facts with the full note, then
		// the short note, then fewer facts. Measured on plain text, painted last.
		var info, note string
	pick:
		for _, parts := range [][]string{
			{asleep, span, "midpoint " + stats.ClockText(mid)},
			{asleep, span},
			{asleep},
		} {
			for _, nt := range []string{"(stages not recorded)", "(no stages)"} {
				info, note = strings.Join(parts, "   "), nt
				if runeLen(info)+2+runeLen(nt) <= cells {
					break pick
				}
			}
		}
		pad := max(cells-runeLen(info)-runeLen(note), 2)
		return []string{sleepIndent + bar, sleepIndent + fit(info, cells-runeLen(note)-2, g) + strings.Repeat(" ", pad) + st.dim(note)}
	}
	segs := []segment{
		{"awake", int64(n.Awake)}, {"rem", int64(n.REM)}, {"core", int64(n.Core)}, {"deep", int64(n.Deep)},
	}
	legend := fmt.Sprintf("%c awake %s  %c REM %s  %c core %s  %c deep %s",
		g.stages["awake"], formatDuration(n.Awake), g.stages["rem"], formatDuration(n.REM),
		g.stages["core"], formatDuration(n.Core), g.stages["deep"], formatDuration(n.Deep))
	return []string{sleepIndent + g.stackedBarPainted(segs, cells, st.stage), sleepIndent + st.dim(fit(legend, cells, g))}
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
	last func(n stats.SleepNight, wide bool) string
	val  func(a *stats.SleepAgg) *float64
	// weekText overrides the average column text (default: from val).
	weekText func(a *stats.SleepAgg, wide bool) string
}

func secsText(s float64) string { return formatDuration(time.Duration(s * float64(time.Second))) }

// stageSum is the total of the stage totals. Stages that overlap each other
// are counted in full there, so dividing by Asleep (the union) could pass 100%.
func stageSum(n stats.SleepNight) time.Duration {
	return n.Core + n.Deep + n.REM + n.Unspecified
}

// stagePct is " 21%": the stage's share of the stage totals.
func stagePct(part, total time.Duration) string {
	if total <= 0 {
		return ""
	}
	return fmt.Sprintf(" %.0f%%", float64(part)/float64(total)*100)
}

// awakenings is the count after the awake time: " / 2 awakenings" with room,
// " 2 wakes" without.
func awakenings(n int, wide bool) string {
	switch {
	case wide && n == 1:
		return " / 1 awakening"
	case wide:
		return fmt.Sprintf(" / %d awakenings", n)
	case n == 1:
		return " 1 wake"
	}
	return fmt.Sprintf(" %d wakes", n)
}

func clockCell(f func(stats.SleepNight) (float64, bool)) func(stats.SleepNight, bool) string {
	return func(n stats.SleepNight, _ bool) string {
		if v, ok := f(n); ok {
			return stats.ClockText(v)
		}
		return ""
	}
}

var sleepStats = []sleepStat{
	{name: "Asleep", compact: true, kind: kindDuration, polarity: +1, minDelta: 5,
		last: func(n stats.SleepNight, _ bool) string { return ifThen(n.Asleep > 0, formatDuration(n.Asleep)) },
		val:  func(a *stats.SleepAgg) *float64 { return a.AsleepSecs }},
	{name: "In bed", compact: true, kind: kindDuration, polarity: 0, minDelta: 5,
		last: func(n stats.SleepNight, _ bool) string { return ifThen(n.InBed > 0, formatDuration(n.InBed)) },
		val:  func(a *stats.SleepAgg) *float64 { return a.InBedSecs }},
	{name: "Efficiency", compact: true, kind: kindPercent, polarity: +1, minDelta: 2,
		last: func(n stats.SleepNight, _ bool) string {
			if p, _, ok := n.Efficiency(); ok {
				return fmt.Sprintf("%.0f%%", p)
			}
			return ""
		},
		val: func(a *stats.SleepAgg) *float64 { return a.Efficiency }},
	{name: "Awake", compact: true, kind: kindDuration, polarity: -1, minDelta: 10,
		last: func(n stats.SleepNight, wide bool) string {
			return ifThen(n.HasStages(), formatDuration(n.Awake)+awakenings(n.Awakenings, wide))
		},
		val: func(a *stats.SleepAgg) *float64 { return a.AwakeSecs },
		weekText: func(a *stats.SleepAgg, wide bool) string {
			if a.AwakeSecs == nil || a.Awakenings == nil {
				return ""
			}
			return secsText(*a.AwakeSecs) + awakenings(int(math.Round(*a.Awakenings)), wide)
		}},
	{name: "Latency", kind: kindDuration, polarity: -1, minDelta: 10,
		last: func(n stats.SleepNight, _ bool) string { return ifThen(n.Latency > 0, "~"+formatDuration(n.Latency)) },
		val:  func(a *stats.SleepAgg) *float64 { return a.LatencySecs }},
	{name: "Deep", compact: true, kind: kindDuration, polarity: +1, minDelta: 5,
		last: func(n stats.SleepNight, _ bool) string {
			return ifThen(n.HasStages(), formatDuration(n.Deep)+stagePct(n.Deep, stageSum(n)))
		},
		val: func(a *stats.SleepAgg) *float64 { return a.DeepSecs }},
	{name: "REM", compact: true, kind: kindDuration, polarity: +1, minDelta: 5,
		last: func(n stats.SleepNight, _ bool) string {
			return ifThen(n.HasStages(), formatDuration(n.REM)+stagePct(n.REM, stageSum(n)))
		},
		val: func(a *stats.SleepAgg) *float64 { return a.REMSecs }},
	{name: "Core", kind: kindDuration, polarity: 0, minDelta: 5,
		last: func(n stats.SleepNight, _ bool) string {
			return ifThen(n.HasStages(), formatDuration(n.Core)+stagePct(n.Core, stageSum(n)))
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
			if *week == 0 { // 0 against 0 (no awake time at all) is as typical as any other row
				return sleepVerdict{text: g.flat + " typical", verdict: "typical"}, true
			}
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
	// Too long (over 9.5 h) or too quick falling asleep (under 5 min) is not "good".
	polarity := s.polarity
	switch {
	case s.name == "Asleep" && (*week > 9.5*3600 || *usual > 9.5*3600):
		polarity = 0
	case s.name == "Latency" && *week < 5*60:
		polarity = 0
	}
	// Direction words. Rows with no good direction say what happened, in plain
	// text: later / earlier for clock times, longer / shorter for durations,
	// higher / lower for the rest.
	word, verdict := map[bool]string{true: "higher", false: "lower"}[delta > 0], "changed"
	switch {
	case s.kind == kindClock:
		word = map[bool]string{true: "later", false: "earlier"}[delta > 0]
	case polarity != 0 && (delta > 0) == (polarity > 0):
		word, verdict = "better", "better"
	case polarity != 0:
		word, verdict = "worse", "worse"
	case s.kind == kindDuration:
		word = map[bool]string{true: "longer", false: "shorter"}[delta > 0]
	}
	return sleepVerdict{text: fmt.Sprintf("%s %+.0f%s %s", arrow, shown, unit, word), verdict: verdict}, true
}

// verdict colours a verdict: green better, red worse, plain when changed (no
// good direction), dim when typical. The words carry the meaning without colour.
func (st style) verdict(v, s string) string {
	switch v {
	case "better":
		return st.green(s)
	case "worse":
		return st.red(s)
	case "changed", "later", "earlier":
		return s
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

	// Column widths by terminal width. "wide" has room for the awake row as
	// "17m / 2 awakenings"; narrower tables say "17m 2 wakes".
	nameW, lastW, weekW := 13, 20, 20
	wide := width >= 80
	switch {
	case width < 60: // "Efficiency" and "Sleep debt" are 10 wide: keep a space after them
		nameW, lastW, weekW = 11, 12, 12
	case width < 80:
		nameW, lastW, weekW = 11, 12, 12
	}
	needed := stats.MinSleepNights
	haveUsual := r.Usual != nil && r.Usual.Nights >= needed

	weekHead := fmt.Sprintf("%d NIGHTS", r.Week.Nights)
	if r.Week.Nights == 1 {
		weekHead = "1 NIGHT"
	}
	usualHead := "VS USUAL"
	if width >= 60 && haveUsual {
		usualHead = fmt.Sprintf("VS %d-NIGHT USUAL", r.Usual.Nights) // the nights really there
	}
	lines := []string{st.dim(sleepIndent + padRight("STAT", nameW) + padRight("LAST NIGHT", lastW) + padRight(weekHead, weekW) + usualHead)}

	var missing []string
	for _, s := range sleepStats {
		if !opts.Full && !s.compact {
			continue
		}
		lastText := s.last(last, wide)
		weekText := ""
		if s.weekText != nil {
			weekText = s.weekText(r.Week, wide)
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
				need := fmt.Sprintf("need %d nights (have %d)", needed, r.Usual.Nights)
				if width < 60 {
					need = fmt.Sprintf("needs %d nights", needed)
				}
				vs = st.dim(need)
			}
		} else if v, ok := compareSleep(s, s.val(r.Week), s.val(r.Usual), g); ok {
			vs = st.verdict(v.verdict, v.text)
		}
		lastCell := padRight(lastText, lastW)
		if lastText == "" { // earlier nights have it, last night does not: a dash, not a blank
			lastCell = st.dim(padRight(g.flat, lastW))
		}
		lines = append(lines, sleepIndent+padRight(s.name, nameW)+lastCell+padRight(weekText, weekW)+vs)
	}

	if opts.Full {
		lines = append(lines, regularityLine(r, g, st, width, nameW, lastW+weekW)...)
	}
	if line := debtLine(r, target, nameW, width); line != "" {
		lines = append(lines, line)
	}
	if opts.Full && last.Nap > 0 {
		lines = append(lines, st.dim(sleepIndent+"nap "+formatDuration(last.Nap)+", counted in asleep"))
	}
	if len(missing) > 0 {
		names := strings.ReplaceAll(strings.Join(missing, ", "), "rem", "REM")
		lines = append(lines, wrapDim("not available: "+names, width, st)...)
	}
	if _, clamped, _ := last.Efficiency(); clamped {
		lines = append(lines, wrapDim("efficiency capped at 100%: sleep and in-bed sources differ", width, st)...)
	}
	if opts.Full && haveUsual {
		lines = append(lines, sleepIndent+markerLegend(st, width-2))
	}
	return lines
}

// wrapDim writes a dim note, wrapped at word boundaries (never cut with an
// ellipsis) onto lines no wider than width.
func wrapDim(text string, width int, st style) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && len(sleepIndent)+runeLen(line)+1+runeLen(word) > width {
			lines = append(lines, st.dim(sleepIndent+line))
			line = "  " + word // continuation lines are indented
			continue
		}
		if line == "" {
			line = word
		} else {
			line += " " + word
		}
	}
	return append(lines, st.dim(sleepIndent+line))
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

// regularityLine is "Regularity  ±38m bed ±31m mid (14 nights)  ▼ better than usual (±52m)".
// The numbers start under LAST NIGHT and the verdict under VS USUAL, so the row
// lines up with the table above it (cols is the width of those two columns).
func regularityLine(r *stats.SleepReport, g glyphSet, st style, width, nameW, cols int) []string {
	reg := r.Regularity
	if reg == nil {
		return nil
	}
	pm := map[bool]string{true: "±", false: "+/-"}[g.dot == "·"]
	text := fmt.Sprintf("%s%.0fm bed %s%.0fm mid", pm, reg.BedtimeMin, pm, reg.MidpointMin)
	withNights := text + fmt.Sprintf(" (%d nights)", reg.Nights)

	verdict, wide, short := "", "", ""
	if reg.UsualBedtimeMin > 0 {
		diff := reg.BedtimeMin - reg.UsualBedtimeMin
		word, v, arrow := "same", "typical", g.flat
		switch {
		case diff <= -5:
			word, v, arrow = "better", "better", g.down
		case diff >= 5:
			word, v, arrow = "worse", "worse", g.up
		}
		// "worse than usual (±39m)", "same as usual (±39m)"; shorter when narrow.
		than := "than"
		if v == "typical" {
			than = "as"
		}
		verdict = v
		wide = fmt.Sprintf("%s %s %s usual (%s%.0fm)", arrow, word, than, pm, reg.UsualBedtimeMin)
		short = fmt.Sprintf("%s %s (usual %s%.0fm)", arrow, word, pm, reg.UsualBedtimeMin)
	}

	room := width - len(sleepIndent) - nameW // what is left for the two columns
	for _, c := range []struct{ numbers, vs string }{
		{withNights, wide}, {text, wide}, {text, short}, {text, ""},
	} {
		cell := c.numbers
		if c.vs != "" {
			cell = padRight(c.numbers+" ", cols) // at least one space before the verdict
		}
		if runeLen(cell)+runeLen(c.vs) <= room {
			line := sleepIndent + padRight("Regularity", nameW) + cell
			if c.vs != "" {
				line += st.verdict(verdict, c.vs)
			}
			return []string{line}
		}
	}
	return []string{sleepIndent + padRight("Regularity", nameW) + fit(text, room, g)}
}

// debtLine compares sleep with the target: "59m short last night, 7 nights: 4h 20m short vs 8h target".
// It uses the longest wording that fits the width.
func debtLine(r *stats.SleepReport, target time.Duration, nameW, width int) string {
	d := r.Debt
	if d == nil {
		return ""
	}
	tgt := strings.Replace(formatDuration(target), " 00m", "", 1)
	last := debtWords(d.LastSecs)
	window := ""
	if d.Nights > 1 {
		window = fmt.Sprintf(", %s: %s", plural(int64(d.Nights), "night"), debtWords(d.WindowSecs))
	}
	room := width - len(sleepIndent) - nameW
	cands := []string{
		last + " last night" + window + " vs " + tgt + " target",
		last + window + " (" + tgt + " target)",
		last + window,
		last,
	}
	text := cands[len(cands)-1]
	for _, c := range cands {
		if runeLen(c) <= room {
			text = c
			break
		}
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
		bandLine("CALENDAR", fmt.Sprintf("time asleep per day, last %s", plural(int64(len(nights)), "night")), width-1, g, st),
	}
	var days, hours, bars strings.Builder
	for _, n := range nights {
		t, _ := time.Parse(stats.DayLayout, n.Night)
		days.WriteString(padRight(t.Format("Mon"), 4))
		hours.WriteString(padRight(fmt.Sprintf("%.0fh", n.Asleep.Hours()), 4))
		level := min(int(math.Round(n.Asleep.Hours()/10*float64(len(g.bars)-1))), len(g.bars)-1)
		bars.WriteString(padRight(strings.Repeat(string(g.bars[max(level, 0)]), 3), 4))
	}
	lines = append(lines, strings.TrimRight(days.String(), " "), strings.TrimRight(hours.String(), " "), st.stage("unspecified", strings.TrimRight(bars.String(), " ")))
	return lines
}
