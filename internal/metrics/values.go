package metrics

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Limits for a typed or imported value.
const (
	// maxValue is the generous upper cap for metrics without a natural limit.
	// No real reading (steps in a day, kcal, ms) comes near it.
	maxValue = 1e7
	// minBloodOxygen is the lowest blood oxygen we accept. Below it the user
	// almost certainly typed a fraction (0.97) instead of a percent (97).
	minBloodOxygen = 50
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

// CheckValue reports why a value (already in the canonical unit) cannot be a
// real reading of m, or nil when it can. It is for values typed by hand.
func (m Metric) CheckValue(v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("value is not a finite number")
	}
	if m.IsPercent() && v > 100 {
		return fmt.Errorf("%s is a percentage and cannot be above 100", m.Name)
	}
	if m.Name == "oxygen_saturation" && v < minBloodOxygen {
		return fmt.Errorf("%s below %d%% is not plausible; type 97 for 97%%", m.Name, minBloodOxygen)
	}
	if v > maxValue {
		return fmt.Errorf("value is too large for %s", m.Name)
	}
	return nil
}
