package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/nmutto/healthcli/internal/stats"
)

// renderToday turns a report into the text "healthcli today" prints. It does no
// I/O, so a test can build a report by hand and compare the exact text.
func renderToday(r *todayReport, st style) string {
	var b strings.Builder

	if r.Status == statusNoData {
		b.WriteString("No health data yet.\n")
		b.WriteString("Import an Apple Health export:  healthcli import apple <export.zip>\n")
		b.WriteString("or log a reading by hand:       healthcli log weight 72.5\n")
		return b.String()
	}
	if r.Status == statusFallback {
		fmt.Fprintf(&b, "%s\n\n", st.yellow(fmt.Sprintf(
			"No data for %s; showing latest day with data: %s", r.RequestedDate, r.Date)))
	}
	fmt.Fprintf(&b, "%s\n\n", st.bold(dayTitle(r.Date)))

	// Plain-text cells first so columns can be padded to a common width.
	labelW, valueW, baseW, cmpW := 0, 0, 0, 0
	type row struct{ label, value, base, cmp, tag string }
	rows := make([]row, 0, len(r.Metrics))
	for _, m := range r.Metrics {
		rw := row{label: m.Label, value: "-"}
		if m.Value != nil {
			rw.value = formatValue(*m.Value, m.Unit) + " " + unitLabel(m.Metric, m.Unit)
			rw.base, rw.cmp, rw.tag = baselineCells(m)
		}
		labelW, valueW = max(labelW, len(rw.label)), max(valueW, len([]rune(rw.value)))
		baseW, cmpW = max(baseW, len([]rune(rw.base))), max(cmpW, len([]rune(rw.cmp)))
		rows = append(rows, rw)
	}
	for _, rw := range rows {
		line := padRight(rw.label, labelW) + "  " + padRight(rw.value, valueW)
		if rw.base != "" {
			line += "  " + st.dim(padRight(rw.base, baseW))
			if rw.cmp != "" {
				line += "  " + padRight(rw.cmp, cmpW) + "  " + labelColor(st, rw.tag)
			}
		}
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}

	b.WriteString("\n" + st.bold("Sleep") + "\n")
	b.WriteString(sleepLines(r.Sleep))

	b.WriteString("\n" + st.bold("Workouts") + "\n")
	if len(r.Workouts) == 0 {
		b.WriteString("  none\n")
	}
	for _, w := range r.Workouts {
		b.WriteString("  " + workoutLine(w) + "\n")
	}
	return b.String()
}

// dayTitle is "2026-11-08 (Sunday)".
func dayTitle(day string) string {
	t, err := time.Parse(stats.DayLayout, day)
	if err != nil {
		return day
	}
	return fmt.Sprintf("%s (%s)", day, t.Weekday())
}

// baselineCells returns the baseline text, the delta text and the label
// (above/below/typical, or "" when there is no comparison).
func baselineCells(m todayMetric) (base, cmp, label string) {
	if m.Baseline == nil {
		return "", "", ""
	}
	if !m.Baseline.Available || m.Baseline.Mean == nil || m.Comparison == nil {
		return fmt.Sprintf("baseline n/a (need %d days)", m.Baseline.DaysNeeded), "", ""
	}
	c := m.Comparison
	return "baseline " + formatValue(*m.Baseline.Mean, m.Unit),
		fmt.Sprintf("%s (%s)", formatSigned(c.Delta, m.Unit), formatPct(c.DeltaPct)), c.Label
}

// labelColor colors the comparison word. Whether "above" is good depends on
// the metric (more steps good, higher resting heart rate not), so the colors
// only say "different from usual", never "good" or "bad".
func labelColor(st style, label string) string {
	switch label {
	case stats.Above, stats.Below:
		return st.cyan(label)
	default:
		return st.dim(label)
	}
}

func sleepLines(s *todaySleep) string {
	if s == nil {
		return "  no sleep recorded\n"
	}
	asleep := sec(s.AsleepSeconds)
	parts := []string{}
	for _, p := range []struct {
		name string
		secs int64
	}{
		{"core", s.CoreSeconds}, {"deep", s.DeepSeconds}, {"REM", s.REMSeconds},
		{"unspecified", s.UnspecifiedSeconds}, {"awake", s.AwakeSeconds}, {"in bed", s.InBedSeconds},
	} {
		if p.secs > 0 {
			parts = append(parts, p.name+" "+formatDuration(sec(p.secs)))
		}
	}
	line := "  Asleep " + formatDuration(asleep)
	if len(parts) > 0 {
		line += "  (" + strings.Join(parts, ", ") + ")"
	}
	return line + "\n"
}

func sec(s int64) time.Duration { return time.Duration(s) * time.Second }

// workoutLine is e.g. "Running  07:30  45m  6.2 km  412 kcal".
func workoutLine(w todayWorkout) string {
	parts := []string{titleCase(w.Activity)}
	if t, err := time.Parse(time.RFC3339, w.Start); err == nil {
		parts = append(parts, t.Format("15:04"))
	}
	parts = append(parts, formatDuration(sec(w.DurationSeconds)))
	if w.DistanceM != nil {
		parts = append(parts, fmt.Sprintf("%.1f km", *w.DistanceM/1000))
	}
	if w.EnergyKcal != nil {
		parts = append(parts, formatInt(*w.EnergyKcal)+" kcal")
	}
	return strings.Join(parts, "  ")
}

// titleCase upper-cases the first letter: "running" -> "Running".
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
