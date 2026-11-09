package cli

import (
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

// heatColors is one hue (teal to bright cyan, 256-colour codes) from dim to
// bright, indexed by level-1. Colour is only a hint: the glyph already says
// the level, and the legend lists the glyphs.
var heatColors = [stats.HeatLevels]string{"38;5;23", "38;5;30", "38;5;37", "38;5;44", "38;5;51"}

// heatWeeksFit is how many week columns fit in width columns. One extra
// column is kept free for the "today" marker at the end of a row.
func heatWeeksFit(width int) int {
	return max((width-heatLabelWidth-1)/heatCellWidth, 1)
}

// shade is the glyph of level 1..5: five of the eight bar heights, evenly
// spread, so the Unicode and ASCII sets both work without new glyphs.
func (g glyphSet) shade(level int) rune {
	return g.bars[[stats.HeatLevels]int{0, 2, 4, 6, 7}[level-1]]
}

// renderHeatmap draws the grid, month labels, "today" marker and legend.
// If h has more weeks than fit in ti.width, the oldest are dropped (callers
// should build only as many weeks as fit, so the legend matches the cells).
func renderHeatmap(h *stats.Heatmap, ti termInfo) []string {
	g := glyphsFor(ti.unicode)
	st := style{color: ti.color}
	weeks := h.Weeks
	if n := heatWeeksFit(ti.width); len(weeks) > n {
		weeks = weeks[len(weeks)-n:]
	}
	if len(weeks) == 0 {
		return nil
	}

	lines := []string{
		fmt.Sprintf("%s per day, %s to %s", strings.ReplaceAll(h.Metric, "_", " "), plural(int64(len(weeks)), "week"), h.Today),
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
			case cell.Value == nil:
				b.WriteString(st.dim(string(g.gap)))
			default:
				glyph := st.wrap(heatColors[cell.Level-1], string(g.shade(cell.Level)))
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
	line := []rune(strings.Repeat(" ", heatLabelWidth+heatCellWidth*len(weeks)))
	prev, end := -1, 0
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
			continue
		}
		name := t.Format("Jan")
		copy(line[pos:], []rune(name))
		end = pos + len(name) + 1
	}
	return strings.TrimRight(string(line), " ")
}

// heatLegend lists each shade with the real upper bound of its level, then
// what the other symbols mean.
func heatLegend(h *stats.Heatmap, ti termInfo, st style) []string {
	g := glyphsFor(ti.unicode)
	le, gt := "≤", ">"
	if !ti.unicode {
		le = "<="
	}
	unit := unitLabel(h.Metric, h.Unit)
	var parts []string
	for i := 1; i <= stats.HeatLevels; i++ {
		glyph := st.wrap(heatColors[i-1], string(g.shade(i)))
		if i < stats.HeatLevels {
			parts = append(parts, glyph+" "+le+formatValue(h.Thresholds[i-1], h.Unit))
		} else {
			parts = append(parts, glyph+" "+gt+formatValue(h.Thresholds[i-2], h.Unit))
		}
	}
	return []string{
		strings.Join(parts, "  "),
		st.dim(string(g.gap)) + " no data   " + todayMark(ti.unicode) + " today   " + unit + " per day",
	}
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
the lowest shade. Today is marked.

The metric defaults to step_count. --weeks sets the maximum number of weeks
(default 12); fewer are shown if the terminal is too narrow (oldest dropped).

JSON output (--json):
  metric, unit, today, from, thresholds (4 upper bounds of levels 1-4),
  days_with_data, weeks[7 days each]: day, value (null = no data), level
  (1-5; 0 = no data), future (true after today)`,
		Example: `  healthcli heatmap
  healthcli heatmap active_energy --weeks 26
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

func runHeatmap(cmd *cobra.Command, opts *options, name string, weeks int, noColor, ascii bool) error {
	if weeks < 1 || weeks > stats.MaxHeatmapWeeks {
		return fmt.Errorf("--weeks must be between 1 and %d, got %d", stats.MaxHeatmapWeeks, weeks)
	}
	// Check the name before opening the database (friendly list on a typo).
	m, err := metrics.Lookup(name)
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
	h, err := stats.BuildHeatmap(cmdContext(cmd), conn, m.Name, weeks, nowFunc())
	if err != nil {
		return err
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(h)
	}
	if h.Empty() {
		_, err = fmt.Fprintf(out, "No %s data in the last %s yet.\n", strings.ReplaceAll(m.Name, "_", " "), plural(int64(weeks), "week"))
		return err
	}
	_, err = fmt.Fprintln(out, strings.Join(renderHeatmap(h, ti), "\n"))
	return err
}
