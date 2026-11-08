package cli

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// renderTrend turns a report into the text "healthcli trend" prints. Like
// renderToday it does no I/O.
func renderTrend(r *trendReport, st style) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", st.bold(fmt.Sprintf("%s, last %d days (%s to %s)",
		trendTitle(r.Metric), r.Days, r.From, r.To)))

	if r.Status == statusNoData {
		fmt.Fprintf(&b, "\nNo %s data in this window.\n", trendTitle(r.Metric))
		if r.LatestDay != "" {
			fmt.Fprintf(&b, "Latest data is from %s; try:  healthcli trend %s --days %d --date %s\n",
				r.LatestDay, r.Metric, r.Days, r.LatestDay)
		} else {
			b.WriteString("Import an Apple Health export with  healthcli import apple <export.zip>  or use  healthcli log.\n")
		}
		return b.String()
	}

	values := make([]float64, len(r.Daily))
	for i, d := range r.Daily {
		values[i] = math.NaN() // NaN = gap, drawn as a dot
		if d.Value != nil {
			values[i] = *d.Value
		}
	}
	fmt.Fprintf(&b, "\n%s\n\n", st.cyan(sparkline(values)))

	s := r.Summary
	fmt.Fprintf(&b, "min %s   max %s   mean %s   (%d of %d days with data)\n",
		fv(r, s.Min), fv(r, s.Max), fv(r, s.Mean), s.DaysWithData, r.Days)

	c := r.Change
	if c.Available {
		fmt.Fprintf(&b, "Last 7 days vs previous 7: %s vs %s  (%s, %s)\n",
			fv(r, *c.Last7Mean), fv(r, *c.PrevMean), fd(r, *c.Delta), formatPct(*c.DeltaPct))
	} else {
		b.WriteString("Last 7 days vs previous 7: n/a (need data in both weeks)\n")
	}

	if r.Baseline.Available {
		fmt.Fprintf(&b, "Baseline (28 days): %s\n", fv(r, *r.Baseline.Mean))
	} else {
		fmt.Fprintf(&b, "Baseline (28 days): n/a (need %d days, have %d)\n", r.Baseline.DaysNeeded, r.Baseline.DaysWithData)
	}

	if r.verbose {
		b.WriteString("\n" + trendTable(r, st))
	}
	return b.String()
}

// trendTable lists every day of the window, oldest first.
func trendTable(r *trendReport, st style) string {
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
		line := fmt.Sprintf("%-10s  %s  %s", d.Day, padLeft(cells[i], valueW), d.Source)
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

// trendTitle is the heading word for a metric: "step_count" -> "Steps".
func trendTitle(metric string) string {
	for _, tm := range todayMetrics {
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
	return formatValue(v, r.Unit) + " " + unitLabel(r.Metric, r.Unit)
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
	return formatSigned(delta, r.Unit) + " " + unitLabel(r.Metric, r.Unit)
}
