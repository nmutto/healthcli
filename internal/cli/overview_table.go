package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nmutto/healthcli/internal/metrics"
	"github.com/nmutto/healthcli/internal/stats"
)

// This file draws the metric table: an index line, then one band per
// category (a rule with the category name and a summary) followed by the
// rows. A row is: gutter marker, label, latest value, change vs usual
// (with the baseline band), trend line and, when it matters, the age.

// gutterW is the room for the "! + ~" attention marker in front of a row.
const gutterW = 2

// narrowAge is the terminal width below which the AGE column is dropped. A
// stale row then carries its age inside the "vs usual" cell instead.
const narrowAge = 76

// shortBand is the terminal width below which a band summary says only counts.
const shortBand = 64

// sleepName and workoutsName are --category names that are not registry categories.
const (
	sleepName    = "sleep"
	workoutsName = "workouts"
)

// categoryNames is every valid --category value, in display order.
func categoryNames() []string {
	return []string{string(metrics.Vitals), string(metrics.Activity), string(metrics.Body),
		string(metrics.Mobility), string(metrics.Other), sleepName, workoutsName}
}

// parseCategories lower-cases and validates the --category list. The error
// names every valid category, so a typo is easy to fix.
func parseCategories(args []string) ([]string, error) {
	var out []string
	for _, a := range args {
		name := strings.ToLower(strings.TrimSpace(a))
		if name == "" {
			continue
		}
		if !slices.Contains(categoryNames(), name) {
			return nil, fmt.Errorf("unknown category %q\nvalid categories: %s", a, strings.Join(categoryNames(), ", "))
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// indexLine is the table of contents under the headline:
//
//	VITALS 9 !1 · ACTIVITY 9 · SLEEP  (not tracked: BODY, MOBILITY, WORKOUTS)
//
// "!n" counts the rows that are worse. Empty categories are named once, in the
// "not tracked" tail. It returns "" when there is nothing to list.
func indexLine(o *stats.Overview, v overviewView, ti termInfo, g glyphSet, st style) string {
	var parts, missing []string
	have := map[string]bool{}
	for _, c := range o.Categories {
		have[c.Name] = true
		if !v.wants(c.Name) {
			continue
		}
		text := fmt.Sprintf("%s %d", strings.ToUpper(c.Name), c.Total)
		if c.Worse > 0 {
			text += fmt.Sprintf(" !%d", c.Worse)
		}
		parts = append(parts, text)
	}
	// Registry categories in display order; "other" is a catch-all, so it is
	// listed when present but never called "not tracked".
	for _, c := range []metrics.Category{metrics.Vitals, metrics.Activity, metrics.Body, metrics.Mobility} {
		if !have[string(c)] && v.wants(string(c)) {
			missing = append(missing, strings.ToUpper(string(c)))
		}
	}
	if v.wants(sleepName) {
		if o.Sleep.State != stats.StateNoData {
			parts = append(parts, "SLEEP")
		} else {
			missing = append(missing, "SLEEP")
		}
	}
	if v.wants(workoutsName) {
		if o.Workouts.Count > 0 {
			parts = append(parts, "WORKOUTS")
		} else {
			missing = append(missing, "WORKOUTS")
		}
	}
	line := strings.Join(parts, " "+g.dot+" ")
	if len(missing) > 0 {
		if line != "" {
			line += "  "
		}
		tail := "(not tracked: " + strings.Join(missing, ", ") + ")"
		if runeLen(line+tail) > ti.width-2 { // narrow pane: count them instead
			tail = fmt.Sprintf("(%d not tracked)", len(missing))
		}
		line += tail
	}
	return fit(line, ti.width-2, g)
}

// tableCols is everything the rows of one table share: which columns are
// shown and how wide they are.
type tableCols struct {
	gutter  int // 0 in compact mode
	labelW  int
	latestW int
	areaW   int // the "vs usual" cell, with the baseline band when it fits
	sparkW  int // 0 = no trend column
	ageW    int // 0 = no age column
	band    bool
	foldAge bool // stale rows put their age in the "vs usual" cell
}

// metricTable draws the header and, per category, a band and its rows.
func metricTable(o *stats.Overview, ti termInfo, g glyphSet, st style, v overviewView) string {
	var rows []stats.MetricRow
	for _, r := range o.Metrics {
		if v.wants(r.Category) {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return ""
	}
	cols := planColumns(rows, ti, g, v.compact)

	// Plain cells are measured first, then painted: invisible colour codes
	// would otherwise count toward the width.
	var b strings.Builder
	head := indent + strings.Repeat(" ", cols.gutter) + padRight("METRIC", cols.labelW) + "  " +
		padRight("LATEST", cols.latestW) + "  " + padRight("VS 28-DAY USUAL", cols.areaW)
	if cols.sparkW > 0 {
		head += "  " + padRight(fmt.Sprintf("%d DAYS", cols.sparkW), cols.sparkW)
	}
	if cols.ageW > 0 {
		head += "  AGE"
	}
	b.WriteString(st.dim(strings.TrimRight(head, " ")) + "\n")

	var current string
	for _, r := range rows {
		if r.Category != current {
			current = r.Category
			if !v.compact {
				b.WriteString("\n")
			}
			b.WriteString(categoryBand(current, summaryFor(o.Categories, current), ti, g, st))
		}
		b.WriteString(metricRow(r, cols, ti, g, st))
	}
	return b.String()
}

// planColumns decides which columns exist and how wide they are.
func planColumns(rows []stats.MetricRow, ti termInfo, g glyphSet, compact bool) tableCols {
	var c tableCols
	if !compact {
		c.gutter = gutterW
	}
	c.foldAge = ti.width < narrowAge

	// The label and latest columns are as wide as their widest cell. Latest
	// is capped so one freak value cannot widen the table, and capped harder
	// on narrow terminals (units are cut with "…" first).
	latestCap := min(maxLatestW, max(ti.width-(len(indent)+c.gutter+12+2+2+deltaW), 6))
	c.labelW, c.latestW = len("METRIC"), len("LATEST")
	for _, r := range rows {
		c.labelW = max(c.labelW, runeLen(r.Label))
		c.latestW = max(c.latestW, runeLen(fit(latestPlain(r, ti), latestCap, g)))
	}
	if !c.foldAge {
		for _, r := range rows {
			c.ageW = max(c.ageW, runeLen(ageCell(r)))
		}
		if c.ageW > 0 {
			c.ageW = max(c.ageW, len("AGE"))
		}
	}
	ageCol := 0
	if c.ageW > 0 {
		ageCol = 2 + c.ageW
	}

	// Long registry labels must not push the line past the terminal: cut the
	// label to what is left once the other columns have their minimum.
	c.labelW = min(c.labelW, max(ti.width-(len(indent)+c.gutter+2+c.latestW+2+deltaW+ageCol), 12))

	// Width budget: drop the trend first when the line gets too long, then
	// the baseline band.
	fixed := len(indent) + c.gutter + c.labelW + 2 + c.latestW + 2 + deltaW + ageCol
	c.band = true
	c.sparkW = min(ti.width-(fixed+2+bandW)-2, stats.SparkDays)
	if c.sparkW < minSpark {
		c.sparkW = 0
		c.band = fixed+2+bandW <= ti.width
	}
	c.areaW = deltaW // the delta+band area, which also holds state words
	if c.band {
		c.areaW += 2 + bandW
	}
	return c
}

// ageCell is the age shown in the AGE column: empty for a reading from today
// or yesterday (the normal case), since only older readings need a flag.
func ageCell(r stats.MetricRow) string {
	if r.AgeDays < 2 && r.State != stats.StateStale {
		return ""
	}
	return ageText(r)
}

// latestPlain is the latest value with its unit, "-" when there is none.
func latestPlain(r stats.MetricRow, ti termInfo) string {
	if r.Value == nil {
		return "-"
	}
	return withUnit(*r.Value, r.Metric, r.Unit, ti.unicode)
}

// latestCell is the padded latest-value cell: the number plain, the unit dim
// so the number stands out.
func latestCell(r stats.MetricRow, width int, ti termInfo, g glyphSet, st style) string {
	text := fit(latestPlain(r, ti), width, g)
	numLen := 0
	if r.Value != nil {
		numLen = runeLen(formatValue(*r.Value, r.Metric))
	}
	runes := []rune(text)
	numLen = min(numLen, len(runes))
	return string(runes[:numLen]) + st.dim(string(runes[numLen:])) + strings.Repeat(" ", max(width-len(runes), 0))
}

// gutterMark is the attention marker of a row: "! " worse, "+ " better,
// "~ " changed, blank otherwise. ASCII on purpose, so it needs no fallback.
func gutterMark(r stats.MetricRow, st style) string {
	if r.State != stats.StateOK {
		return "  "
	}
	switch r.Verdict {
	case stats.VerdictWorse:
		return st.red("!") + " "
	case stats.VerdictBetter:
		return st.green("+") + " "
	case stats.VerdictChanged:
		return st.cyan("~") + " "
	}
	return "  "
}

// metricRow draws one metric.
func metricRow(r stats.MetricRow, c tableCols, ti termInfo, g glyphSet, st style) string {
	line := indent
	if c.gutter > 0 {
		line += gutterMark(r, st)
	}
	line += padRight(fit(r.Label, c.labelW, g), c.labelW) + "  " + latestCell(r, c.latestW, ti, g, st) + "  "
	line += deltaArea(r, g, st, c.band, c.areaW, c.foldAge)
	if c.sparkW > 0 {
		line += "  " + padRight(sparkText(r, c.sparkW, g), c.sparkW)
	}
	if c.ageW > 0 {
		line += "  " + colorIf(r.State == stats.StateStale, st.yellow, ageCell(r))
	}
	return strings.TrimRight(line, " ") + "\n"
}

// summaryFor finds the category's counts (nil if the report has none).
func summaryFor(all []stats.CategorySummary, name string) *stats.CategorySummary {
	for i := range all {
		if all[i].Name == name {
			return &all[i]
		}
	}
	return nil
}

// bandSummary is the right half of a band: "9 metrics · 1 better · 1 worse".
// Zero counts are left out; "all typical" when nothing moved. short drops the
// word "metrics" for narrow terminals.
func bandSummary(c *stats.CategorySummary, g glyphSet, short bool) string {
	if c == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("%d", c.Total)}
	if !short {
		parts[0] += " metric"
		if c.Total != 1 {
			parts[0] += "s"
		}
	}
	moved := false
	for _, x := range []struct {
		n    int
		word string
	}{{c.Better, "better"}, {c.Worse, "worse"}, {c.Changed, "changed"}} {
		if x.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", x.n, x.word))
			moved = true
		}
	}
	switch {
	case !moved && c.Typical > 0:
		parts = append(parts, "all typical")
	case !moved && c.Stale == 0:
		parts = append(parts, "not enough data")
	}
	if c.Stale > 0 {
		parts = append(parts, fmt.Sprintf("%d stale", c.Stale))
	}
	return strings.Join(parts, " "+g.dot+" ")
}

// categoryBand is one full-width line: "━━ VITALS ━━ 9 metrics · 1 worse ━━━━".
// The rule is dim, the name bold; colour is kept for verdicts.
func categoryBand(name string, c *stats.CategorySummary, ti termInfo, g glyphSet, st style) string {
	rule := string(g.bandRule)
	title := strings.ToUpper(name)
	total := ti.width - len(indent) - 1 // same right edge as the title rule
	// Visible width of everything but the summary and the closing rule:
	// "━━ NAME ━━ ", plus a space before the closing rule.
	room := total - (2 + 1 + runeLen(title) + 1 + 2 + 1) - 1
	summary := fit(bandSummary(c, g, ti.width < shortBand), room, g)

	b := indent + st.dim(strings.Repeat(rule, 2)) + " " + st.bold(title) + " " + st.dim(strings.Repeat(rule, 2))
	used := 2 + 1 + runeLen(title) + 1 + 2
	if summary != "" {
		b += " " + summary
		used += 1 + runeLen(summary)
	}
	if fill := total - used - 1; fill > 0 {
		b += " " + st.dim(strings.Repeat(rule, fill))
	}
	return b + "\n"
}

// tableFooter explains the colours and the gutter. It is plain text, so it
// also helps when colour is off (the words in the rows are the same either way).
func tableFooter(o *stats.Overview, st style, ti termInfo, g glyphSet) string {
	if ti.width-2 < 60 { // too wide for a narrow pane: say it shorter
		return indent + st.green("green") + st.dim(" better ") + st.red("red") + st.dim(" worse ") + st.cyan("cyan") + st.dim(" changed") + "\n"
	}
	sep := st.dim(" / ")
	return indent + st.green("green") + st.dim(" better") + sep + st.red("red") + st.dim(" worse") + sep +
		st.cyan("cyan") + st.dim(" changed (no good direction)") + "\n"
}
