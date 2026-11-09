package cli

import (
	"fmt"
	"math"
	"slices"
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

	compact    bool     // --compact: no blank lines, no gutter
	categories []string // --category: only these (canonical names); nil = all
	heatmaps   []string // --heatmap: steps/sleep/workouts/metric names asked for
	full       bool     // --full: every section and heatmap
	heatWeeks  int      // weeks of every heatmap; 0 = the default (12)
	noHeat     bool     // --weeks 0: no heatmaps at all
}

// section is one named block of the overview. renderOverview builds the list
// and prints the blocks in order, so adding a section is adding one entry.
type section struct {
	name string // metrics, sleep, workouts, heatmap, data
	text func() string
}

// Fixed column widths of the metric table. The label, value and sparkline
// columns are sized from the data and the terminal width instead.
const (
	deltaW     = 16 // "▲ +17% better", at most "▼ <-999% changed"; also fits the header
	bandW      = 10 // the baseline band
	maxLatestW = 18 // longer values are cut, so one freak number cannot widen the table
	minSpark   = 14 // a trend line shorter than two weeks is not worth drawing
	indent     = " "
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

	b.WriteString(indent + headline(o, g, ti.width-2) + "\n")
	if idx := indexLine(o, v, ti, g, st); idx != "" {
		b.WriteString(indent + idx + "\n")
	}
	for i, sec := range overviewSections(o, ti, g, st, v) {
		text := sec.text()
		if v.compact && i > 0 { // --compact: no blank line between sections
			text = strings.TrimPrefix(text, "\n")
		}
		b.WriteString(text)
	}
	return b.String()
}

// overviewSections lists the blocks to print, in order: metric tables, sleep,
// workouts, heatmaps, data. Every block starts with its own blank line.
// --category hides every block it does not name, and also the heatmaps and
// DATA unless asked for.
func overviewSections(o *stats.Overview, ti termInfo, g glyphSet, st style, v overviewView) []section {
	filtered := len(v.categories) > 0
	secs := []section{
		{"metrics", func() string {
			if len(o.Metrics) == 0 { // e.g. a database with only sleep has no table
				return ""
			}
			return "\n" + metricTable(o, ti, g, st, v) + tableFooter(o, st, ti, g)
		}},
	}
	// A single section picked with --category gets its full version.
	alone := len(v.categories) == 1 && (v.wants("sleep") != v.wants("workouts"))
	full := v.full || alone
	if v.wants("sleep") {
		secs = append(secs, section{"sleep", func() string {
			opts := sleepOpts{Full: full, NoHeatmap: v.ownHeatmapsHidden()}
			return linesBlock(renderSleepSection(o.SleepSummary, ti, ti.width, opts))
		}})
	}
	if v.wants("workouts") {
		secs = append(secs, section{"workouts", func() string {
			opts := workoutsOpts{Full: full, Weeks: v.weeks(ti), NoHeatmap: v.ownHeatmapsHidden()}
			if o.WorkoutsReport == nil { // no workouts: no section, not even a "none" line
				return ""
			}
			return linesBlock(renderWorkoutsSection(o.WorkoutsReport, ti, ti.width, opts))
		}})
	}
	if !filtered || len(v.heatmaps) > 0 {
		secs = append(secs, section{"heatmap", func() string { return heatmapBlocks(o, ti, g, st, v) }})
	}
	if !filtered {
		secs = append(secs, section{"data", func() string {
			out := "\n" + dataBlock(o.Data, ti, g, st, v)
			if n := len(o.NotTracked); n > 0 {
				out += indent + st.dim(fmt.Sprintf("not tracked in your data: %s", plural(int64(n), "metric"))) + "\n"
			}
			return out
		}})
	}
	return secs
}

// ownHeatmapsHidden is true when the full sleep and workouts sections must not
// draw their own heatmap: it was asked for (--heatmap) and is drawn in the
// heatmap area instead, or heatmaps are off (--weeks 0).
func (v overviewView) ownHeatmapsHidden() bool { return v.noHeat || len(v.heatmaps) > 0 }

// weeks is the length of the heatmaps the renderer draws itself.
func (v overviewView) weeks(ti termInfo) int {
	w := v.heatWeeks
	if w == 0 {
		w = defaultHeatWeeks
	}
	return min(w, heatWeeksFit(ti.width-len(indent)))
}

// linesBlock is a section made of lines, led by a blank line; nothing for no lines.
func linesBlock(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return "\n" + strings.Join(lines, "\n") + "\n"
}

// heatmapBlocks draws the heatmap area. With --heatmap it draws exactly the
// named calendars, in the order given; without it the activity calendar, and
// in --full also the sleep and workouts calendars (inside their sections).
func heatmapBlocks(o *stats.Overview, ti termInfo, g glyphSet, st style, v overviewView) string {
	if v.noHeat {
		return ""
	}
	if len(v.heatmaps) == 0 {
		return heatmapBlock(o.Heatmap, ti, st)
	}
	var out string
	for _, name := range v.heatmaps {
		switch name {
		case sleepName:
			if o.SleepSummary != nil {
				out += linesBlock(sleepCalendar(o.SleepSummary, ti, ti.width))
			}
		case workoutsName:
			if o.WorkoutsReport != nil {
				out += linesBlock(wkHeatmap(o.WorkoutsReport, ti, ti.width, workoutsOpts{Weeks: v.weeks(ti)}))
			}
		default:
			out += heatmapBlock(o.Heatmaps[name], ti, st)
		}
	}
	return out
}

// wants reports whether a category (or sleep / workouts) is selected.
func (v overviewView) wants(name string) bool {
	if len(v.categories) == 0 {
		return true
	}
	return slices.Contains(v.categories, name)
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

// headline is the one-sentence summary at the top. Metrics with a good
// direction are counted as better / worse; the others, when beyond usual, as
// "changed".
func headline(o *stats.Overview, g glyphSet, width int) string {
	counts := map[string]int{}
	var mover *stats.MetricRow // the differing metric with the largest |z|
	waiting := false           // some metric still lacks history for a baseline
	newest := -1               // age of the freshest reading, -1 = none
	for i := range o.Metrics {
		r := &o.Metrics[i]
		switch r.State {
		case stats.StateOK:
			counts[r.Verdict]++
			if r.Differs() {
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
	var counted []string
	for _, v := range []string{stats.VerdictBetter, stats.VerdictWorse, stats.VerdictChanged, stats.VerdictTypical} {
		if counts[v] > 0 {
			counted = append(counted, fmt.Sprintf("%d %s", counts[v], v))
		}
	}
	switch {
	case len(counted) > 0:
		text := fmt.Sprintf("%s vs your %d-day usual", strings.Join(counted, ", "), stats.DefaultBaselineDays)
		if runeLen(text) > width { // narrow terminal: say it shorter instead of cutting it
			text = strings.Join(counted, ", ") + " vs usual"
		}
		parts = append(parts, text)
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
	if math.IsNaN(r) {
		return "-"
	}
	return fmt.Sprintf("%+d%%", int(r))
}

// colorIf applies paint to s when cond holds.
func colorIf(cond bool, paint func(string) string, s string) string {
	if cond {
		return paint(s)
	}
	return s
}

// verdictPaint is the colour decision: what colour a verdict gets.
//
//	better  -> green    worse -> red    changed -> cyan    typical -> none
//
// "changed" is a metric beyond its usual that has no good direction
// (neutral polarity). Polarity is a general guide, not medical advice.
func verdictPaint(st style, verdict string) func(string) string {
	switch verdict {
	case stats.VerdictBetter:
		return st.green
	case stats.VerdictWorse:
		return st.red
	case stats.VerdictChanged:
		return st.cyan
	}
	return func(s string) string { return s }
}

// verdictWord is the text that backs the colour up: better, worse or changed
// (typical rows are plain). Colour is never the only signal.
func verdictWord(verdict string) string {
	if verdict == stats.VerdictBetter || verdict == stats.VerdictWorse || verdict == stats.VerdictChanged {
		return verdict
	}
	return ""
}

// deltaArea is the "vs usual" area of a row, painted and padded to areaW.
// Only the arrow, delta and word are coloured, plus the marker in the band,
// never the whole row. Colour never carries meaning alone: the arrow and the
// word say it too.
func deltaArea(r stats.MetricRow, g glyphSet, st style, showBand bool, areaW int, foldAge bool) string {
	switch r.State {
	case stats.StateOK:
		paint := verdictPaint(st, r.Verdict)
		text := g.deltaArrow(*r.DeltaPct) + " " + pctText(*r.DeltaPct)
		if w := verdictWord(r.Verdict); w != "" {
			text += " " + w
		}
		out := paint(text)
		used := runeLen(text)
		if showBand {
			out += strings.Repeat(" ", deltaW-used+2)
			band := g.bandBar(*r.Value, *r.Mean, *r.Stddev, bandW)
			out += strings.Replace(band, string(g.marker), paint(string(g.marker)), 1)
			used = deltaW + 2 + bandW
		}
		return out + strings.Repeat(" ", max(areaW-used, 0))
	case stats.StateNoBaseline:
		return st.dim(padRight(fit(fmt.Sprintf("need %dd (have %d)", stats.MinBaselineDays, r.BaselineDays), areaW, g), areaW))
	case stats.StateStale:
		word := "stale"
		if foldAge { // no AGE column at this width
			word += " " + ageText(r)
		}
		return st.yellow(padRight(word, areaW))
	}
	return st.dim(padRight("no data", areaW))
}

// heatmapBlock is the activity calendar, titled and indented like the other
// sections. Nothing is printed when there is no heatmap.
func heatmapBlock(h *stats.Heatmap, ti termInfo, st style) string {
	if h == nil {
		return ""
	}
	inner := ti
	inner.width = ti.width - len(indent)
	lines := renderHeatmap(h, inner)
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n" + indent + st.bold("CALENDAR") + " " + lines[0] + "\n")
	for _, l := range lines[1:] {
		// A very wide legend (huge numbers) loses its colours rather than
		// running past the terminal.
		if plain := stripANSI(l); runeLen(plain) > inner.width {
			l = fit(plain, inner.width, glyphsFor(ti.unicode))
		}
		b.WriteString(strings.TrimRight(indent+l, " ") + "\n")
	}
	return b.String()
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
	case r.AgeDays > 9999:
		return ">9999 d"
	}
	return fmt.Sprintf("%d d", r.AgeDays)
}

// dataBlock is the "how complete is this data" footer.
func dataBlock(d stats.DataHealth, ti termInfo, g glyphSet, st style, v overviewView) string {
	label := indent + st.bold(padRight("DATA", sectionLabelW)) + " "
	cont := strings.Repeat(" ", len(indent)+sectionLabelW+1) // lines after the first
	room := ti.width - len(cont) - 1

	lines := []string{plural(d.Samples, "sample")}
	if d.SleepNights > 0 {
		lines[0] += ", " + plural(d.SleepNights, "sleep night")
	}
	if d.Samples > 0 {
		lines = append(lines, "range: "+d.FirstDay+" "+g.arrow+" "+d.LastDay, "sources: "+sourcesText(d.SourceCount))
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

// plural is "1 sample" or "2 samples" (with thousands separators).
func plural(n int64, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return formatInt(float64(n)) + " " + noun + "s"
}

// stripANSI removes colour codes, to measure the visible width of a line.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
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
