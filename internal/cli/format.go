package cli

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nmutto/healthcli/internal/metrics"
)

// style decides whether text gets ANSI colors. The render functions take a
// style (not a writer), so tests can render with colors off and get plain,
// comparable text.
type style struct{ color bool }

// colorFor says whether to color output written to w: only when w is a real
// terminal (not a pipe or file, where escape codes would be garbage) and the
// user has not set NO_COLOR (https://no-color.org). TERM=dumb terminals do
// not understand escape codes either.
func colorFor(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// wrap puts an ANSI "select graphic rendition" code around s.
func (st style) wrap(code, s string) string {
	if !st.color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// The 16-colour ANSI codes the output uses, all in one place. Colour is never
// the only carrier of meaning: arrows and words say the same thing.
const (
	ansiBold   = "1"
	ansiDim    = "2"
	ansiRed    = "1;31" // worse than usual (bold, so it also differs from green in brightness)
	ansiGreen  = "32"   // better than usual
	ansiYellow = "33"   // stale data
)

// Sleep stage colours follow the iOS Health app: orange for awake, light blue
// for REM, blue for core and deep blue for deep sleep (256-colour codes).
// "unspecified" (asleep, no stages) and per-night bars use the core blue.
var stageColors = map[string]string{
	"awake":       "38;5;208",
	"rem":         "38;5;117",
	"core":        "38;5;33",
	"deep":        "38;5;21",
	"unspecified": "38;5;33",
}

// stage paints s in the colour of a sleep stage (plain when colour is off).
func (st style) stage(name, s string) string {
	if code, ok := stageColors[name]; ok {
		return st.wrap(code, s)
	}
	return s
}

func (st style) bold(s string) string   { return st.wrap(ansiBold, s) }
func (st style) dim(s string) string    { return st.wrap(ansiDim, s) }
func (st style) yellow(s string) string { return st.wrap(ansiYellow, s) }
func (st style) green(s string) string  { return st.wrap(ansiGreen, s) }
func (st style) red(s string) string    { return st.wrap(ansiRed, s) }

// formatInt prints a whole number with thousands separators: 12345 -> "12,345".
func formatInt(v float64) string {
	if huge(v) {
		return formatHuge(v)
	}
	n := int64(math.Round(v))
	neg := n < 0
	if neg {
		n = -n
	}
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range digits {
		// A comma goes before every group of three digits, counted from the right.
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// maxPlain is the largest magnitude printed in full; beyond it numbers switch
// to scientific notation so a freak value cannot widen a column (and int64
// conversion cannot overflow).
const maxPlain = 1e15

// huge reports a number that cannot be printed in full: too big, NaN or Inf.
func huge(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) >= maxPlain }

// formatHuge prints such a number: "1.00e+300", or "-" for NaN and Inf.
func formatHuge(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "-"
	}
	return strconv.FormatFloat(v, 'e', 2, 64)
}

// formatDuration prints "45m" under an hour and "7h 12m" from an hour up.
// Rounding to whole minutes first avoids "6h 60m".
func formatDuration(d time.Duration) string {
	mins := int(math.Round(d.Minutes()))
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dh %02dm", mins/60, mins%60)
}

// formatValue prints a metric value with the digits its registry entry calls
// for (Metric.Decimals) and thousands separators (no unit suffix).
func formatValue(v float64, metric string) string {
	if huge(v) {
		return formatHuge(v)
	}
	if d := decimals(metric); d > 0 {
		return strconv.FormatFloat(v, 'f', d, 64)
	}
	return formatInt(v)
}

// decimals is the registry's digits after the point for a metric; an unknown
// name gets 0.
func decimals(metric string) int {
	if m, err := metrics.Lookup(metric); err == nil {
		return m.Decimals
	}
	return 0
}

// metricLabel is the name of a metric as a person reads it: the registry
// Label ("Respiratory rate", "VO2 max"). Anything else (sleep, workouts, an
// unknown name) is returned as given.
func metricLabel(name string) string {
	if m, err := metrics.Lookup(name); err == nil {
		return m.Label
	}
	return name
}

// unitLabel is the unit as stored. Apple's "count" is meaningless to read,
// so step counts say "steps".
func unitLabel(metric, unit string) string {
	if metric == "step_count" {
		return "steps"
	}
	return unit
}

// displayUnit is the unit as a person writes it: "%", "°C", "/min". It is
// display only (stored units never change) and may be empty, as for BMI where
// the label already says it. glued units print with no space: "97%".
func displayUnit(metric, unit string, unicode bool) (text string, glued bool) {
	switch unit {
	case "percent":
		return "%", true
	case "degC":
		if unicode {
			return "°C", false
		}
		return "C", false
	case "breaths/min":
		return "/min", true
	case "kg/m2":
		return "", false
	case "count":
		switch metric {
		case "step_count":
			return "steps", false
		case "flights_climbed":
			return "flights", false
		}
		return "", false
	}
	return unit, false
}

// withUnit is value and display unit together: "97%", "36.4 °C", "7,412 steps".
func withUnit(v float64, metric, unit string, unicode bool) string {
	return joinUnit(formatValue(v, metric), metric, unit, unicode)
}

// joinUnit puts the display unit after an already formatted number.
func joinUnit(s, metric, unit string, unicode bool) string {
	u, glued := displayUnit(metric, unit, unicode)
	switch {
	case u == "":
		return s
	case glued:
		return s + u
	}
	return s + " " + u
}

// formatSigned is formatValue with an explicit + for positive numbers, used
// for differences. A tiny difference that rounds to zero prints without a
// sign ("0"), never "+0" or "-0".
func formatSigned(v float64, metric string) string {
	s := formatValue(v, metric)
	if strings.Trim(s, "0.-") == "" {
		return strings.TrimPrefix(s, "-")
	}
	if v > 0 {
		return "+" + s
	}
	return s
}

// formatPct prints a percentage with one decimal and a sign: "+6.7%".
func formatPct(p float64) string {
	s := strconv.FormatFloat(p, 'f', 1, 64)
	if strings.Trim(s, "0.-") == "" {
		return "0.0%"
	}
	if p > 0 {
		s = "+" + s
	}
	return s + "%"
}
