package cli

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// renderTrend turns a report into the text "healthcli trend" prints. Like
// renderToday it does no I/O. No line is wider than ti.width: long ones are
// wrapped or lose their least important tail.
func renderTrend(r *trendReport, ti termInfo) string {
	st := style{color: ti.color}
	g := glyphsFor(ti.unicode)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", st.bold(trendHeading(r, ti.width)))

	if r.Status == statusNoData {
		b.WriteString("\n")
		hint := fmt.Sprintf("No %s data in this window.", trendTitle(r.Metric))
		if r.LatestDay != "" {
			hint += fmt.Sprintf(" Latest data is from %s; try: healthcli trend %s --days %d --date %s",
				r.LatestDay, r.Metric, r.Days, r.LatestDay)
		} else {
			hint += " Import an Apple Health export with healthcli import apple <export.zip> or use healthcli log."
		}
		b.WriteString(strings.Join(wordWrap(hint, ti.width), "\n") + "\n")
		return b.String()
	}

	values := make([]float64, len(r.Daily))
	for i, d := range r.Daily {
		values[i] = math.NaN() // NaN = gap, drawn as a dot
		if d.Value != nil {
			values[i] = *d.Value
		}
	}
	// One character per day: a long window shows only its newest days.
	shown := values[max(len(values)-ti.width, 0):]
	fmt.Fprintf(&b, "\n%s\n", st.cyan(g.sparkline(shown)))
	if len(shown) < len(values) {
		b.WriteString(st.dim(fmt.Sprintf("(the last %d of %d days)", len(shown), len(values))) + "\n")
	}
	b.WriteString("\n")

	s := r.Summary
	b.WriteString(strings.Join(wrapParts([]string{
		"min " + fv(r, s.Min),
		"max " + fv(r, s.Max),
		"mean " + fv(r, s.Mean),
		fmt.Sprintf("(%d of %d days with data)", s.DaysWithData, r.Days),
	}, "   ", ti.width), "\n") + "\n")

	c := r.Change
	if c.Available {
		last, prev := fv(r, *c.Last7Mean), fv(r, *c.PrevMean)
		change := fmt.Sprintf("(%s, %s)", fd(r, *c.Delta), formatPct(*c.DeltaPct))
		b.WriteString(strings.Join(changeLines(last, prev, change, ti.width), "\n") + "\n")
	} else {
		b.WriteString(strings.Join(wordWrap("Last 7 days vs previous 7: n/a (need data in both weeks)", ti.width), "\n") + "\n")
	}

	if r.Baseline.Available {
		fmt.Fprintf(&b, "Baseline (28 days): %s\n", fv(r, *r.Baseline.Mean))
	} else {
		fmt.Fprintf(&b, "Baseline (28 days): n/a (need %d days, have %d)\n", r.Baseline.DaysNeeded, r.Baseline.DaysWithData)
	}

	if r.verbose {
		b.WriteString("\n" + trendTable(r, st, ti))
	}
	return b.String()
}

// trendHeading is the first line. Details go before the metric name does:
// "Steps, last 30 days (from to to)", then without the dates, then the name.
func trendHeading(r *trendReport, width int) string {
	name := trendTitle(r.Metric)
	heading := name
	for _, cand := range []string{
		fmt.Sprintf("%s, last %d days (%s to %s)", name, r.Days, r.From, r.To),
		fmt.Sprintf("%s, last %d days", name, r.Days),
		name,
	} {
		heading = cand
		if runeLen(cand) <= width {
			break
		}
	}
	return heading
}

// changeLines is the "last 7 days vs previous 7" comparison on one line, or
// on as many short lines as the width needs.
func changeLines(last, prev, change string, width int) []string {
	for _, lines := range [][]string{
		{"Last 7 days vs previous 7: " + last + " vs " + prev + "  " + change},
		{"Last 7 days vs previous 7: " + last + " vs " + prev, "  " + change},
		{"Last 7 days: " + last, "Previous 7:  " + prev, "Change:      " + strings.Trim(change, "()")},
	} {
		fits := true
		for _, l := range lines {
			fits = fits && runeLen(l) <= width
		}
		if fits {
			return lines
		}
	}
	return []string{"Last 7 days: " + last, "Previous 7: " + prev, "Change: " + strings.Trim(change, "()")}
}

// wordWrap breaks text at spaces into lines of at most width characters.
func wordWrap(text string, width int) []string {
	return wrapParts(strings.Fields(text), " ", width)
}

// trendTable lists every day of the window, oldest first.
func trendTable(r *trendReport, st style, ti termInfo) string {
	g := glyphsFor(ti.unicode)
	header := "value"
	if r.Metric != sleepMetric {
		header += " (" + unitLabel(r.Metric, r.Unit) + ")"
	}
	valueW := len(header)
	cells := make([]string, len(r.Daily))
	for i, d := range r.Daily {
		cells[i] = "-"
		if d.Value != nil {
			cells[i] = strings.TrimSuffix(fv(r, *d.Value), " "+unitLabel(r.Metric, r.Unit))
		}
		valueW = max(valueW, len([]rune(cells[i])))
	}
	var b strings.Builder
	b.WriteString(st.dim(fmt.Sprintf("%-10s  %s  %s", "day", padLeft(header, valueW), "source")) + "\n")
	for i, d := range r.Daily {
		// The source is the only part that can be cut to fit.
		room := ti.width - 10 - 2 - valueW - 2
		line := fmt.Sprintf("%-10s  %s  %s", d.Day, padLeft(cells[i], valueW), fit(d.Source, room, g))
		b.WriteString(strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}

func padLeft(s string, n int) string {
	if w := len([]rune(s)); w < n {
		return strings.Repeat(" ", n-w) + s
	}
	return s
}

// trendLabels are the headings of the metrics people ask "trend" about most.
var trendLabels = []struct{ name, label string }{
	{"step_count", "Steps"},
	{"active_energy", "Active energy"},
	{"heart_rate", "Avg heart rate"},
	{"resting_heart_rate", "Resting heart rate"},
	{"hrv_sdnn", "HRV"},
	{"body_mass", "Weight"},
}

// titleCase upper-cases the first letter: "running" -> "Running".
func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// trendTitle is the heading word for a metric: "step_count" -> "Steps".
func trendTitle(metric string) string {
	for _, tm := range trendLabels {
		if tm.name == metric {
			return tm.label
		}
	}
	return titleCase(metric)
}

// fv formats a value of the report's metric with its unit: sleep as
// "7h 12m", the rest as "8,432 steps".
func fv(r *trendReport, v float64) string {
	if r.Metric == sleepMetric {
		return formatDuration(time.Duration(v * float64(time.Hour)))
	}
	return formatValue(v, r.Metric) + " " + unitLabel(r.Metric, r.Unit)
}

// fd formats a signed difference: "+532 steps", "-0.3 kg", "+0h 25m" for sleep.
func fd(r *trendReport, delta float64) string {
	if r.Metric == sleepMetric {
		sign := "+"
		if delta < 0 {
			sign = "-"
		}
		if delta == 0 {
			sign = ""
		}
		return sign + formatDuration(time.Duration(math.Abs(delta)*float64(time.Hour)))
	}
	return formatSigned(delta, r.Metric) + " " + unitLabel(r.Metric, r.Unit)
}
