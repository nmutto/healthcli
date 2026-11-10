package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nmutto/healthcli/internal/metrics"
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
	switch h.Hue {
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
	name := strings.ReplaceAll(heatTitle(h), "_", " ") + " per day"
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
		return h.Title
	}
	return h.Metric
}

func heatFormat(h *stats.Heatmap, v float64) string {
	if h.Format != nil {
		return h.Format(v)
	}
	return formatValue(v, h.Metric)
}

func heatUnit(h *stats.Heatmap) string {
	if h.Format != nil {
		return h.Unit
	}
	return unitLabel(h.Metric, h.Unit)
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
	if u := heatUnit(h); u != "" {
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

// wrapParts joins parts with sep, starting a new line before a part that
// would run past width columns (colour codes do not count).
func wrapParts(parts []string, sep string, width int) []string {
	var lines []string
	line := ""
	for _, p := range parts {
		if line != "" && runeLen(stripANSI(line))+len(sep)+runeLen(stripANSI(p)) > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += sep
		}
		line += p
	}
	return append(lines, line)
}

func newHeatmapCmd(opts *options) *cobra.Command {
	var weeks int
	var noColor, ascii bool
	cmd := &cobra.Command{
		Use:   "heatmap [metric]",
		Short: "Calendar heatmap of one metric, one cell per day",
		Long: `Draw a GitHub-style calendar: one row per weekday (Mon to Sun), one column
per week, newest week on the right. Shades are the metric's own quintiles over
the window (the legend lists the cut points), so the darkest fifth of days is
always the highest fifth for that metric. A day without data shows a dot, not
the lowest shade; for workouts a dot means a rest day. Today is marked.

The metric defaults to step_count; "sleep" (time asleep per day) and
"workouts" (workout minutes per day, with rest days) work too. --weeks sets the
maximum number of weeks (default 12); fewer are shown if the terminal is too
narrow (oldest dropped).

JSON output (--json):
  metric, unit, today, from, thresholds (4 upper bounds of levels 1-4),
  days_with_data, weeks[7 days each]: day, value (left out = no data), level
  (1-5; 0 = no data, or a rest day with value 0 for workouts), future (true after today)`,
		Example: `  healthcli heatmap
  healthcli heatmap active_energy --weeks 26
  healthcli heatmap sleep
  healthcli heatmap workouts --weeks 26
  healthcli heatmap steps --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := "step_count"
			if len(args) == 1 {
				name = args[0]
			}
			return runHeatmap(cmd, opts, name, weeks, noColor, ascii)
		},
	}
	cmd.Flags().IntVar(&weeks, "weeks", defaultHeatWeeks, "maximum number of weeks to show (1-53)")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "never use colours")
	cmd.Flags().BoolVar(&ascii, "ascii", false, "use plain ASCII characters only")
	return cmd
}

// heatmapName resolves the argument: "sleep", "workouts" or a registry metric
// (aliases allowed). The error for a typo also lists sleep and workouts.
func heatmapName(arg string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "sleep":
		return "sleep", nil
	case "workouts", "workout":
		return "workouts", nil
	}
	m, err := metrics.Lookup(arg)
	if err != nil {
		return "", fmt.Errorf("%w\nalso: sleep, workouts", err)
	}
	return m.Name, nil
}

// minutesText writes minutes as "7h 30m" for the legend.
func minutesText(mins float64) string {
	return formatDuration(time.Duration(mins * float64(time.Minute)))
}

// buildNamedHeatmap builds the heatmap for sleep, workouts or a metric. Sleep
// and workouts feed the same engine as steps, just with a different source
// of "one number per day".
func buildNamedHeatmap(ctx context.Context, conn *sql.DB, name string, weeks int, now time.Time) (*stats.Heatmap, error) {
	if name != "sleep" && name != "workouts" {
		return stats.BuildHeatmap(ctx, conn, name, weeks, now)
	}
	if weeks < 1 || weeks > stats.MaxHeatmapWeeks {
		return nil, fmt.Errorf("--weeks must be between 1 and %d, got %d", stats.MaxHeatmapWeeks, weeks)
	}
	// The window starts on the Monday `weeks` calendar weeks back; reading
	// one day more than needed is harmless, so just go back far enough.
	today := now.Format(stats.DayLayout)
	from, err := stats.AddDays(today, -(7*weeks + 6))
	if err != nil {
		return nil, err
	}
	var byDay map[string]float64
	spec := stats.HeatSpec{Title: name, Format: minutesText}
	if name == "sleep" {
		spec.Hue = stats.HueBlue
		byDay, err = stats.SleepAsleepByDay(ctx, conn, from, today)
	} else {
		spec.ZeroIsRest = true
		spec.Hue = stats.HueGreen
		// Read all history: a workout before the window makes the blank days
		// at its start rest days.
		byDay, err = stats.WorkoutMinutesByDay(ctx, conn, "0001-01-01", today)
	}
	if err != nil {
		return nil, err
	}
	return stats.HeatmapFromDays(spec, now, weeks, byDay)
}

func runHeatmap(cmd *cobra.Command, opts *options, name string, weeks int, noColor, ascii bool) error {
	if weeks < 1 || weeks > stats.MaxHeatmapWeeks {
		return fmt.Errorf("--weeks must be between 1 and %d, got %d", stats.MaxHeatmapWeeks, weeks)
	}
	// Check the name before opening the database (friendly list on a typo).
	name, err := heatmapName(name)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	ti := detectTerm(out, os.Getenv, noColor, ascii)
	if !opts.jsonOutput {
		weeks = min(weeks, heatWeeksFit(ti.width))
	}

	conn, err := openDB(opts)
	if err != nil {
		return err
	}
	defer conn.Close()
	h, err := buildNamedHeatmap(cmdContext(cmd), conn, name, weeks, nowFunc())
	if err != nil {
		return err
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(h)
	}
	if h.Empty() {
		what := strings.ReplaceAll(name, "_", " ") + " data"
		if name == "workouts" {
			what = "workouts" // "workouts data" reads wrong
		}
		_, err = fmt.Fprintf(out, "No %s in the last %s yet.\n", what, plural(int64(weeks), "week"))
		return err
	}
	_, err = fmt.Fprintln(out, strings.Join(renderHeatmap(h, ti), "\n"))
	return err
}
