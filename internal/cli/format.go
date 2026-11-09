package cli

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
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
	ansiRed    = "31" // worse than usual
	ansiGreen  = "32" // better than usual
	ansiYellow = "33" // stale data
	ansiCyan   = "36" // changed, and no good direction
)

func (st style) bold(s string) string   { return st.wrap(ansiBold, s) }
func (st style) dim(s string) string    { return st.wrap(ansiDim, s) }
func (st style) cyan(s string) string   { return st.wrap(ansiCyan, s) }
func (st style) yellow(s string) string { return st.wrap(ansiYellow, s) }
func (st style) green(s string) string  { return st.wrap(ansiGreen, s) }
func (st style) red(s string) string    { return st.wrap(ansiRed, s) }

// formatInt prints a whole number with thousands separators: 12345 -> "12,345".
func formatInt(v float64) string {
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

// formatDuration prints "45m" under an hour and "7h 12m" from an hour up.
// Rounding to whole minutes first avoids "6h 60m".
func formatDuration(d time.Duration) string {
	mins := int(math.Round(d.Minutes()))
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dh %02dm", mins/60, mins%60)
}

// decimals is how many digits after the point each unit gets. Counts and
// energy are whole numbers; a heart rate of 61.4 bpm is false precision;
// body weight is shown to 0.1 kg like a bathroom scale does.
func decimals(unit string) int {
	if unit == "kg" {
		return 1
	}
	return 0
}

// formatValue prints a metric value with the digits and separators its unit
// calls for (no unit suffix).
func formatValue(v float64, unit string) string {
	if d := decimals(unit); d > 0 {
		return strconv.FormatFloat(v, 'f', d, 64)
	}
	return formatInt(v)
}

// unitLabel is the unit as printed. Apple's "count" is meaningless to read,
// so step counts say "steps".
func unitLabel(metric, unit string) string {
	if metric == "step_count" {
		return "steps"
	}
	return unit
}

// formatSigned is formatValue with an explicit + for positive numbers, used
// for differences. A tiny difference that rounds to zero prints without a
// sign ("0"), never "+0" or "-0".
func formatSigned(v float64, unit string) string {
	s := formatValue(v, unit)
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

// padRight pads s with spaces to n characters (runes, not bytes, so "·" and
// "▲" count as one). Padding is done on plain text BEFORE coloring, because
// invisible ANSI codes would otherwise count toward the width.
func padRight(s string, n int) string {
	if w := len([]rune(s)); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}
