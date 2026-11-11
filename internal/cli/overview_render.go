package cli

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
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
	oneDay     bool     // "healthcli today": the page is one day, not the latest readings
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

	if h := headline(o, v, g, ti.width-2); h != "" {
		b.WriteString(indent + h + "\n")
	}
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
			// No rows (a database with only sleep, or --category sleep): no
			// header, no legend.
			table := metricTable(o, ti, g, st, v)
			if table == "" {
				return ""
			}
			return "\n" + table + tableFooter(o, st, ti, g)
		}},
	}
	// A single section picked with --category gets its full version.
	alone := len(v.categories) == 1 && (v.wants(sleepName) != v.wants(workoutsName))
	full := v.full || alone
	if v.wants(sleepName) {
		secs = append(secs, section{sleepName, func() string {
			opts := sleepOpts{Full: full, NoHeatmap: v.ownHeatmapsHidden()}
			return linesBlock(renderSleepSection(o.SleepSummary, ti, ti.width, opts))
		}})
	}
	if v.wants(workoutsName) {
		secs = append(secs, section{workoutsName, func() string {
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
	// Calendar days, not 24 h blocks: 23:00 yesterday seen at 08:00 is 1 d ago.
	then = then.In(now.Location())
	thenDay := time.Date(then.Year(), then.Month(), then.Day(), 0, 0, 0, 0, time.UTC)
	nowDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	days := int(nowDay.Sub(thenDay) / (24 * time.Hour))
	if days <= 0 {
		return "today"
	}
	return fmt.Sprintf("%d d ago", days)
}

// headline is the one-sentence summary at the top. Metrics with a good
// direction are counted as better / worse; the others, when beyond usual, as
// "changed". With --category it only counts the categories shown, and says
// nothing about metrics when none are shown; it returns "" for no sentence.
func headline(o *stats.Overview, v overviewView, g glyphSet, width int) string {
	counts := map[string]int{}
	var mover *stats.MetricRow // the differing metric with the largest |z|
	waiting := false           // some metric still lacks history for a baseline
	unusable := false          // some metric has the history, but its baseline is not a usable number
	newest := -1               // age of the freshest reading, -1 = none
	scoped := len(v.categories) > 0
	shown := 0 // metric rows in scope
	for i := range o.Metrics {
		r := &o.Metrics[i]
		if !v.wants(r.Category) {
			continue
		}
		shown++
		switch r.State {
		case stats.StateOK:
			counts[r.Verdict]++
			if r.Differs() {
				if mover == nil || biggerMove(r, mover) {
					mover = r
				}
			}
		case stats.StateNoBaseline:
			if r.BaselineDays >= stats.MinBaselineDays {
				unusable = true
			} else {
				waiting = true
			}
		}
		if r.State != stats.StateNoData && (newest < 0 || r.AgeDays < newest) {
			newest = r.AgeDays
		}
	}

	var parts []string
	var counted []string
	for _, verdict := range []string{stats.VerdictBetter, stats.VerdictWorse, stats.VerdictChanged, stats.VerdictTypical} {
		if counts[verdict] > 0 {
			counted = append(counted, fmt.Sprintf("%d %s", counts[verdict], verdict))
		}
	}
	switch {
	case scoped && shown == 0: // only sleep / workouts asked for: no metric sentence
	case len(counted) > 0:
		text := fmt.Sprintf("%s vs your %d-day usual", strings.Join(counted, ", "), stats.DefaultBaselineDays)
		if runeLen(text) > width { // narrow terminal: say it shorter instead of cutting it
			text = strings.Join(counted, ", ") + " vs usual"
		}
		if runeLen(text) > width { // still too wide: drop "vs usual" rather than cut a word
			text = strings.Join(counted, ", ")
		}
		parts = append(parts, text)
	case waiting:
		parts = append(parts, firstFit(width,
			fmt.Sprintf("Not enough history to compare yet (a baseline needs %d days)", stats.MinBaselineDays),
			"Not enough history to compare yet",
			"Not enough history yet"))
	case unusable:
		parts = append(parts, firstFit(width, "No usable baseline to compare against", "No usable baseline"))
	case newest > 0:
		parts = append(parts, fmt.Sprintf("No recent readings: the newest is %d d old", newest))
	default:
		text := "No metric readings yet"
		if v.oneDay {
			text = "No metric readings on this day"
		}
		parts = append(parts, text)
	}
	if mover != nil {
		parts = append(parts, fmt.Sprintf("%s %s", mover.Label, moveText(*mover)))
	}
	if s := o.Sleep; v.wants(sleepName) && s.State == stats.StateOK && s.AsleepSecs > 0 {
		parts = append(parts, "slept "+formatDuration(time.Duration(s.AsleepSecs)*time.Second))
	}
	if w := o.Workouts; scoped && v.wants(workoutsName) && w.Count > 0 {
		parts = append(parts, fmt.Sprintf("%s in %d d", plural(int64(w.Count), "workout"), w.Days))
	}
	if len(parts) == 0 {
		return ""
	}
	return joinFit(parts, width, g)
}

// biggerMove says whether row a moved more than row b: by |z| first. A flat
// baseline makes every z zero, so ties go to the larger change (percentage
// points for a metric that is itself a percentage, else relative change).
func biggerMove(a, b *stats.MetricRow) bool {
	if za, zb := math.Abs(*a.Z), math.Abs(*b.Z); za != zb {
		return za > zb
	}
	return moveSize(a) > moveSize(b)
}

// moveSize is the size of a row's change, in points or in percent.
func moveSize(r *stats.MetricRow) float64 {
	if inPoints(*r) {
		return math.Abs(*r.Value - *r.Mean)
	}
	if r.DeltaPct != nil {
		return math.Abs(*r.DeltaPct)
	}
	return 0
}

// noBaselineText says why a row has nothing to compare against, in at most
// width characters: too few days of history, or history that is there but
// whose mean overflowed, or a value that overflowed.
func noBaselineText(r stats.MetricRow, width int) string {
	switch {
	case r.Value == nil:
		return firstFit(width, "no usable value", "no value")
	case r.BaselineDays >= stats.MinBaselineDays:
		return firstFit(width, "no usable baseline", "no baseline")
	}
	return fmt.Sprintf("need %dd (have %d)", stats.MinBaselineDays, r.BaselineDays)
}

// firstFit returns the first text that fits width (the last one if none do),
// so a sentence gets shorter instead of being cut in the middle.
func firstFit(width int, texts ...string) string {
	for _, t := range texts {
		if runeLen(t) <= width {
			return t
		}
	}
	return texts[len(texts)-1]
}

// listFit writes "prefix item, item, +N more" on one line of at most room
// characters, dropping whole items from the end (and counting them in "+N
// more") rather than cutting one in the middle. hidden is the number of
// items the caller left out of items before this.
func listFit(prefix string, items []string, hidden, room int) string {
	line := ""
	for n := len(items); n >= 0; n-- {
		line = prefix + strings.Join(items[:n], ", ")
		if more := len(items) - n + hidden; more > 0 {
			if n > 0 {
				line += ", "
			}
			line += fmt.Sprintf("+%d more", more)
		}
		if runeLen(line) <= room {
			break
		}
	}
	return line
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
// deltaText is the arrow and percentage of a metric row. With a baseline mean
// of 0 there is no percentage, so only the direction and "n/a" show.
func deltaText(r stats.MetricRow, g glyphSet) string {
	if inPoints(r) {
		d := math.Round((*r.Value-*r.Mean)*10) / 10 // the digit the text shows
		arrow := g.flat
		switch {
		case d > 0:
			arrow = g.up
		case d < 0:
			arrow = g.down
		}
		return arrow + " " + pointsText(d)
	}
	if r.DeltaPct != nil {
		return g.deltaArrow(*r.DeltaPct) + " " + pctText(*r.DeltaPct)
	}
	arrow := g.flat
	switch {
	case *r.Value > *r.Mean:
		arrow = g.up
	case *r.Value < *r.Mean:
		arrow = g.down
	}
	return arrow + " n/a"
}

// inPoints is true for a metric that is itself a percentage (blood oxygen,
// body fat, walking asymmetry, ...): its change is told in percentage points,
// because a relative change of a small percentage is noise ("1% to 2%" is
// +100%, and says nothing).
func inPoints(r stats.MetricRow) bool {
	return r.Unit == "percent" && r.Value != nil && r.Mean != nil
}

// pointsText is a signed change in percentage points with one digit at most:
// "+1.2pt", "-3pt", "0pt".
func pointsText(d float64) string {
	s := strconv.FormatFloat(math.Round(d*10)/10, 'f', 1, 64)
	s = strings.TrimSuffix(s, ".0")
	switch s {
	case "0", "-0":
		return "0pt"
	}
	if d > 0 {
		s = "+" + s
	}
	return s + "pt"
}

// moveText is the size of a row's change for the headline: a percentage, or
// percentage points for a metric that is itself a percentage.
func moveText(r stats.MetricRow) string {
	if inPoints(r) {
		return pointsText(*r.Value - *r.Mean)
	}
	return pctOrNA(r.DeltaPct)
}

// pctOrNA is pctText for a percentage that may be missing.
func pctOrNA(pct *float64) string {
	if pct == nil {
		return "n/a"
	}
	return pctText(*pct)
}

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
//	better  -> green    worse -> bold red (ansiRed, 1;31)    changed, typical -> none
//
// "changed" is a metric beyond its usual that has no good direction
// (neutral polarity): it says higher or lower in plain text, no colour.
// Colour is a judgment, and only a known direction can judge. Polarity is a
// general guide, not medical advice.
func verdictPaint(st style, verdict string) func(string) string {
	switch verdict {
	case stats.VerdictBetter:
		return st.green
	case stats.VerdictWorse:
		return st.red
	}
	return func(s string) string { return s }
}

// verdictWord is the text that backs the colour up: better or worse, and
// higher or lower for a move with no good direction (typical rows have no
// word). Colour is never the only signal.
func verdictWord(r stats.MetricRow) string {
	switch r.Verdict {
	case stats.VerdictBetter, stats.VerdictWorse:
		return r.Verdict
	case stats.VerdictChanged:
		switch {
		case r.Comparison != nil && *r.Comparison == stats.Above:
			return "higher"
		case r.Comparison != nil && *r.Comparison == stats.Below:
			return "lower"
		}
		return stats.VerdictChanged
	}
	return ""
}

// deltaArea is the "vs usual" area of a row, painted and padded to areaW.
// Only the arrow, delta and word are coloured, plus the marker in the band,
// never the whole row. Colour never carries meaning alone: the arrow and the
// word say it too.
func deltaArea(r stats.MetricRow, g glyphSet, st style, showBand bool, areaW int, foldAge bool) string {
	return deltaAreaIn(r, g, st, showBand, areaW, foldAge, deltaW)
}

// deltaAreaIn is deltaArea with the delta text padded to textW columns before
// the band (a table whose deltas are all short can use a smaller textW).
func deltaAreaIn(r stats.MetricRow, g glyphSet, st style, showBand bool, areaW int, foldAge bool, textW int) string {
	switch r.State {
	case stats.StateOK:
		paint := verdictPaint(st, r.Verdict)
		text := deltaText(r, g)
		if w := verdictWord(r); w != "" {
			text += " " + w
		}
		out := paint(text)
		used := runeLen(text)
		if showBand {
			out += strings.Repeat(" ", textW-used+2)
			band := g.bandBar(*r.Value, *r.Mean, *r.Stddev, bandW)
			out += strings.Replace(band, string(g.marker), paint(string(g.marker)), 1)
			used = textW + 2 + bandW
		}
		return out + strings.Repeat(" ", max(areaW-used, 0))
	case stats.StateNoBaseline:
		return st.dim(padRight(fit(noBaselineText(r, areaW), areaW, g), areaW))
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
	b.WriteString("\n")
	for _, l := range lines {
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

// dataBlock is the "how complete is this data" footer: a band with the sample
// count, then one indented line per fact.
func dataBlock(d stats.DataHealth, ti termInfo, g glyphSet, st style, v overviewView) string {
	room := ti.width - len(indent) - 1

	head := plural(d.Samples, "sample")
	if d.SleepNights > 0 {
		head += ", " + plural(d.SleepNights, "sleep night")
	}
	var lines []string
	if d.Samples > 0 {
		lines = append(lines, "range: "+d.FirstDay+" "+g.arrow+" "+d.LastDay, listFit("sources: ", sourceParts(d.SourceCount), 0, room))
	}
	if li := d.LastImport; li != nil {
		lines = append(lines, importLines(li, g, room)...)
	}
	if len(d.Gaps) > 0 {
		lines = append(lines, gapsLine(d.Gaps, room))
	}

	var b strings.Builder
	b.WriteString(indent + bandLine("DATA", head, room, g, st) + "\n")
	for _, l := range lines {
		b.WriteString(indent + fit(l, room, g) + "\n")
	}
	return b.String()
}

// sourceParts is ["Watch 79%", "iPhone 20%", "manual 1%"]. The percentages come
// from largestRemainder so they always add up to exactly 100.
func sourceParts(counts map[string]int64) []string {
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
	return parts
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

// gapsLine lists up to three metrics with missing days, worst first, and
// fewer on a narrow line ("+N more" counts the rest).
func gapsLine(gaps []stats.Gap, room int) string {
	sorted := append([]stats.Gap(nil), gaps...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Missing > sorted[j].Missing })
	var parts []string
	for _, x := range sorted[:min(len(sorted), 3)] {
		parts = append(parts, fmt.Sprintf("%s %d of %d", x.Label, x.Missing, x.Span))
	}
	return listFit("days missing: ", parts, len(sorted)-len(parts), room)
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
