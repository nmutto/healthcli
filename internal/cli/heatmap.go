package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/nmutto/healthcli/internal/stats"
)

// This file draws the calendar heatmap. renderHeatmap is pure (a grid in,
// lines out) so tests can render it at any width without a database.
// Nothing here is wired into "overview" yet.

const (
	defaultHeatWeeks = 12
	heatLabelWidth   = 4 // "Mon "
	heatCellWidth    = 2 // a glyph and a space
)

var heatDayNames = [7]string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

// Each kind of heatmap has its own hue, from dim (level 1) to bright (level 5),
// as 256-colour codes. Colour is only a hint: the glyph already says the
// level, and the legend lists the glyphs.
var (
	heatOrange = [stats.HeatLevels]string{"38;5;94", "38;5;130", "38;5;166", "38;5;208", "38;5;214"} // steps and other activity
	heatGreen  = [stats.HeatLevels]string{"38;5;22", "38;5;28", "38;5;34", "38;5;40", "38;5;46"}     // workouts
	heatBlue   = [stats.HeatLevels]string{"38;5;24", "38;5;26", "38;5;33", "38;5;75", "38;5;117"}    // sleep: one azure hue, smoothly dark to light (not the stage colours)
	heatRed    = [stats.HeatLevels]string{"38;5;52", "38;5;88", "38;5;124", "38;5;160", "38;5;196"}  // heart rate
	heatCyan   = [stats.HeatLevels]string{"38;5;23", "38;5;30", "38;5;37", "38;5;44", "38;5;51"}     // respiratory rate
	heatPurple = [stats.HeatLevels]string{"38;5;54", "38;5;91", "38;5;128", "38;5;165", "38;5;201"}  // wrist temperature
	heatViolet = [stats.HeatLevels]string{"38;5;53", "38;5;56", "38;5;61", "38;5;93", "38;5;99"}     // body mass (deep violet)
)

// heatColorsFor picks the ramp from the heatmap's hue: workouts are green,
// sleep is blue, heart rate red, respiratory rate cyan, wrist temperature
// purple, body mass deep violet, and everything else (steps, energy, ...) is orange.
func heatColorsFor(h *stats.Heatmap) [stats.HeatLevels]string {
	return heatRamp(h.Hue)
}

// heatRamp is the five-step colour ramp of a hue (dim at level 1, bright at
// level 5), shared by the heatmaps and the trend line.
func heatRamp(hue string) [stats.HeatLevels]string {
	switch hue {
	case stats.HueGreen:
		return heatGreen
	case stats.HueBlue:
		return heatBlue
	case stats.HueRed:
		return heatRed
	case stats.HueCyan:
		return heatCyan
	case stats.HuePurple:
		return heatPurple
	case stats.HueViolet:
		return heatViolet
	}
	return heatOrange
}

// heatWeeksFit is how many week columns fit in width columns. One extra
// column is kept free for the "today" marker at the end of a row.
func heatWeeksFit(width int) int {
	return max((width-heatLabelWidth-1)/heatCellWidth, 1)
}

// shade is the glyph of level 1..5: five of the eight bar heights, evenly
// spread, so the Unicode and ASCII sets both work without new glyphs.
//
// With colour on, every level is the same full block and only the colour
// tells them apart: blocks of different heights make the grid look ragged.
// Without colour (pipes, NO_COLOR, --no-color) the heights are the only
// signal left, so they stay.
func (g glyphSet) shade(level int, coloured bool) rune {
	if coloured {
		return g.block
	}
	return g.bars[[stats.HeatLevels]int{0, 2, 4, 6, 7}[level-1]]
}

// renderHeatmap draws the grid, month labels, "today" marker and legend.
// If h has more weeks than fit in ti.width, the oldest are dropped (callers
// should build only as many weeks as fit, so the legend matches the cells).
func renderHeatmap(h *stats.Heatmap, ti termInfo) []string {
	g := glyphsFor(ti.unicode)
	st := style{color: ti.color}
	colors := heatColorsFor(h)
	weeks := h.Weeks
	if n := heatWeeksFit(ti.width); len(weeks) > n {
		weeks = weeks[len(weeks)-n:]
	}
	if len(weeks) == 0 {
		return nil
	}

	// The first line is a band like every other section; the title gets
	// shorter before it would be cut.
	name := heatTitle(h) + " per day"
	total := ti.width - 1
	title := name
	for _, cand := range []string{
		fmt.Sprintf("%s, %s to %s", name, plural(int64(len(weeks)), "week"), h.Today),
		fmt.Sprintf("%s, %s", name, plural(int64(len(weeks)), "week")),
		name,
	} {
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
			isToday := cell.Day == h.Today
			switch {
			case cell.Future:
				b.WriteString("  ")
				continue
			case cell.Value == nil && h.ZeroIsRest:
				b.WriteString(" ") // before the first value: blank, not a gap
			case cell.Value == nil:
				b.WriteString(st.dim(string(g.gap)))
			case cell.Level == 0:
				b.WriteString(st.dim(string(restGlyph(g)))) // a real zero: a rest day
			default:
				glyph := st.wrap(colors[cell.Level-1], string(g.shade(cell.Level, st.color)))
				b.WriteString(glyph)
			}
			b.WriteString(" ")
			if isToday {
				marker = todayMark(ti.unicode)
			}
		}
		line := strings.TrimRight(b.String(), " ")
		if marker != "" {
			line += " " + st.bold(marker)
		}
		lines = append(lines, line)
	}

	lines = append(lines, "")
	lines = append(lines, heatLegend(h, ti, st)...)
	return lines
}

// heatTitle, heatFormat and heatUnit read the Heatmap's own labels. A map built
// by BuildHeatmap (a registry metric) has none, so those fall back to the
// metric name.
func heatTitle(h *stats.Heatmap) string {
	if h.Title != "" {
		return metricLabel(h.Title)
	}
	return metricLabel(h.Metric)
}

func heatFormat(h *stats.Heatmap, v float64) string {
	if h.Format != nil {
		return h.Format(v)
	}
	return formatValue(v, h.Metric)
}

func heatUnit(h *stats.Heatmap, unicode bool) string {
	if h.Format != nil {
		return h.Unit
	}
	u, _ := displayUnit(h.Metric, h.Unit, unicode)
	return u
}

// restGlyph marks a rest day (a real zero). In Unicode the gap is already a
// dot (and rest maps never draw gaps). In ASCII the gap is '_' and the lowest
// level is '.', so a rest day gets ':' to stay different from both.
func restGlyph(g glyphSet) rune {
	if g.gap == '_' {
		return ':'
	}
	return g.gap
}

// heatTail is the last legend line (or two on a narrow terminal): what the
// other symbols mean.
func heatTail(h *stats.Heatmap, ti termInfo, st style) []string {
	g := glyphsFor(ti.unicode)
	var marks string
	if h.ZeroIsRest {
		marks = st.dim(string(restGlyph(g))) + " rest day"
	} else {
		marks = st.dim(string(g.gap)) + " no data"
	}
	unit := "per day"
	if u := heatUnit(h, ti.unicode); u != "" {
		unit = u + " per day"
	}
	return wrapParts([]string{marks, todayMark(ti.unicode) + " today", unit}, "   ", ti.width)
}

// todayMark is the symbol shown after today's cell.
func todayMark(unicode bool) string {
	if unicode {
		return "◂"
	}
	return "<"
}

// heatMonthLine puts a three-letter month name above the first week column
// of each month, skipping a name that would run into the previous one.
func heatMonthLine(weeks [][]stats.HeatDay) string {
	line := []rune(strings.Repeat(" ", heatLabelWidth+heatCellWidth*len(weeks)+3)) // +3: room for a name in the last column
	prev, end, lastPos := -1, 0, -1
	for i, week := range weeks {
		// The first Monday-to-Sunday column that contains day 1 of a month,
		// or the very first column, carries the label.
		t, err := time.Parse(stats.DayLayout, week[0].Day)
		if err != nil {
			continue
		}
		month := int(t.Month())
		for _, c := range week {
			if ct, err := time.Parse(stats.DayLayout, c.Day); err == nil && ct.Day() == 1 {
				month, t = int(ct.Month()), ct
			}
		}
		if month == prev {
			continue
		}
		prev = month
		pos := heatLabelWidth + heatCellWidth*i
		if pos < end {
			// Too close to the previous label. The first column is only a
			// partial month (its label is a guess), so the newer month wins
			// there; otherwise the new label is skipped.
			if lastPos != heatLabelWidth {
				continue
			}
			for k := lastPos; k < end; k++ {
				line[k] = ' '
			}
		}
		lastPos = pos
		name := t.Format("Jan")
		copy(line[pos:], []rune(name))
		end = pos + len(name) + 1
	}
	return strings.TrimRight(string(line), " ")
}

// heatLegend lists each shade with the real upper bound of its level, then
// what the other symbols mean. Lines wrap onto more lines when the terminal
// is too narrow for them.
func heatLegend(h *stats.Heatmap, ti termInfo, st style) []string {
	colors := heatColorsFor(h)
	g := glyphsFor(ti.unicode)
	le, gt := "≤", ">"
	if !ti.unicode {
		le = "<="
	}
	if h.Flat && len(h.Thresholds) > 0 {
		// Every day has the same value: one shade, one sentence, no "≤0 ≤0 ≤0".
		level := 3
		if h.Thresholds[0] == 0 {
			level = 1
		}
		return append([]string{
			st.wrap(colors[level-1], string(g.shade(level, st.color))) + " every day: " + heatFormat(h, h.Thresholds[0]),
		}, heatTail(h, ti, st)...)
	}
	// Only levels some day has are listed (with few days or ties some are
	// unreachable), and a bound that prints like the one before it is skipped.
	used := heatLevelsUsed(h.Weeks)
	var parts []string
	prev := ""
	for i := 1; i <= stats.HeatLevels; i++ {
		if !used[i] {
			continue
		}
		glyph := st.wrap(colors[i-1], string(g.shade(i, st.color)))
		var bound string
		if i < stats.HeatLevels {
			bound = le + heatFormat(h, h.Thresholds[i-1])
		} else {
			bound = gt + heatFormat(h, h.Thresholds[i-2])
		}
		if bound == prev {
			continue
		}
		prev = bound
		parts = append(parts, glyph+" "+bound)
	}
	return append(wrapParts(parts, "  ", ti.width), heatTail(h, ti, st)...)
}

// heatLevelsUsed reports which levels (1..5) at least one cell has.
func heatLevelsUsed(weeks [][]stats.HeatDay) [stats.HeatLevels + 1]bool {
	var used [stats.HeatLevels + 1]bool
	for _, week := range weeks {
		for _, c := range week {
			used[c.Level] = true
		}
	}
	return used
}

// minutesText writes minutes as "7h 30m" for the legend.
func minutesText(mins float64) string {
	return formatDuration(time.Duration(mins * float64(time.Minute)))
}
