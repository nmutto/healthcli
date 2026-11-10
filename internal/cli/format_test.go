package cli

import (
	"testing"
	"time"
)

// Display units and per-metric decimals (stored units are untouched).
func TestWithUnit(t *testing.T) {
	cases := []struct {
		v            float64
		metric, unit string
		unicode      bool
		want         string
	}{
		{36.44, "apple_sleeping_wrist_temperature", "degC", true, "36.4 °C"},
		{36.44, "apple_sleeping_wrist_temperature", "degC", false, "36.4 C"},
		{97, "oxygen_saturation", "percent", true, "97%"},
		{16.24, "respiratory_rate", "breaths/min", true, "16.2/min"},
		{24.06, "body_mass_index", "kg/m2", true, "24.1"},
		{79.94, "body_mass", "kg", true, "79.9 kg"},
		{9412, "step_count", "count", true, "9,412 steps"},
		{42, "vo2_max", "mL/kg/min", true, "42.0 mL/kg/min"},
	}
	for _, c := range cases {
		if got := withUnit(c.v, c.metric, c.unit, c.unicode); got != c.want {
			t.Errorf("withUnit(%v, %s) = %q, want %q", c.v, c.metric, got, c.want)
		}
	}
}

func TestFormatting(t *testing.T) {
	if got := formatInt(1234567); got != "1,234,567" {
		t.Errorf("formatInt = %q", got)
	}
	if got := formatInt(-999.6); got != "-1,000" {
		t.Errorf("formatInt negative = %q", got)
	}
	for d, want := range map[time.Duration]string{
		45 * time.Minute:                              "45m",
		7*time.Hour + 12*time.Minute:                  "7h 12m",
		7*time.Hour + 59*time.Second + 45*time.Minute: "7h 46m",
		5*time.Hour + 59*time.Minute + 40*time.Second: "6h 00m", // no "5h 60m"
	} {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
	}
	if got := formatSigned(-0.04, "body_mass"); got != "0.0" {
		t.Errorf("formatSigned tiny = %q", got)
	}
}
