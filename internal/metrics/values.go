package metrics

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseNumber reads a plain decimal number, with an optional exponent.
// ok is false for anything else. strconv.ParseFloat alone would also take hex
// floats ("0x1p6"), underscores ("1_000"), "NaN" and "Inf", none of which is a
// measurement. The importer and `healthcli log` both read numbers through this.
func ParseNumber(s string) (f float64, ok bool) {
	if s == "" || strings.Trim(s, "0123456789+-.eE") != "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// IsPercent reports whether the metric is stored as a percentage (0 to 100).
func (m Metric) IsPercent() bool { return m.Unit == "percent" }

// Plausible reports why v (already in the canonical unit) cannot be a real
// reading of m, or nil when it can. Both `healthcli log` and the importer
// call it; the bounds live in the registry (Metric.Limits).
func (m Metric) Plausible(v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("value is not a finite number")
	}
	l := m.Limits
	if v > l.Max {
		if m.IsPercent() {
			return fmt.Errorf("%s is a percentage and cannot be above %g", m.Name, l.Max)
		}
		return fmt.Errorf("value is too large for %s (the most accepted is %g %s)", m.Name, l.Max, m.Unit)
	}
	if v < l.Min || (l.ExcludeMin && v == l.Min) {
		if m.IsPercent() && l.Min > 0 {
			// Someone typed a fraction (0.97) instead of a percent (97).
			return fmt.Errorf("%s below %g%% is not plausible; type 97 for 97%%", m.Name, l.Min)
		}
		if l.ExcludeMin {
			return fmt.Errorf("%s must be more than %g %s", m.Name, l.Min, m.Unit)
		}
		return fmt.Errorf("%s must be at least %g %s", m.Name, l.Min, m.Unit)
	}
	return nil
}
