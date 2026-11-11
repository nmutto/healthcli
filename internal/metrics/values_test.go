package metrics

import (
	"fmt"
	"math"
	"testing"
)

func TestParseNumber(t *testing.T) {
	good := map[string]float64{"12": 12, "-1.5": -1.5, "1e3": 1000, ".5": 0.5, "+4": 4}
	for in, want := range good {
		if got, ok := ParseNumber(in); !ok || got != want {
			t.Errorf("ParseNumber(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "0x1p6", "1_0", "NaN", "Inf", "-inf", "1e999", "abc", "1 0"} {
		if _, ok := ParseNumber(in); ok {
			t.Errorf("ParseNumber(%q) should fail", in)
		}
	}
}

func TestPlausible(t *testing.T) {
	spo2, _ := Lookup("spo2")
	steps, _ := Lookup("steps")
	fat, _ := Lookup("body_fat_percentage")
	hr, _ := Lookup("hr")
	temp, _ := Lookup("temp")
	cases := []struct {
		m    Metric
		v    float64
		fail bool
	}{
		{spo2, 97, false}, {spo2, 50, false}, {spo2, 49.9, true}, {spo2, 0.97, true}, {spo2, 100.5, true},
		{fat, 0, false}, {fat, 100, false}, {fat, 100.1, true}, {fat, -1, true},
		{steps, 0, false}, {steps, 1e6, false}, {steps, 1e6 + 1, true}, {steps, -1, true},
		{hr, 0, true}, {hr, 0.1, false}, {hr, 300, false}, {hr, 9999, true},
		{temp, -0.4, false}, {temp, 0, false}, {temp, 46, true},
		{hr, math.NaN(), true}, {hr, math.Inf(1), true},
	}
	for _, c := range cases {
		if err := c.m.Plausible(c.v); (err != nil) != c.fail {
			t.Errorf("%s Plausible(%v) = %v, fail=%v", c.m.Name, c.v, err, c.fail)
		}
	}
}

// The review table for review: every metric's accepted values, the same
// rule for `log` and the importer. "(" means the lower end itself is refused.
// A change to a bound in the registry must change this table too.
func TestLimitsTable(t *testing.T) {
	want := map[string]string{
		"heart_rate":                       "(0, 300]",
		"resting_heart_rate":               "(0, 250]",
		"step_count":                       "[0, 1e+06]",
		"hrv_sdnn":                         "(0, 2000]",
		"body_mass":                        "(0, 500]",
		"active_energy":                    "[0, 20000]",
		"respiratory_rate":                 "(0, 100]",
		"apple_sleeping_wrist_temperature": "[-15, 45]",
		"oxygen_saturation":                "[50, 100]",
		"vo2_max":                          "(0, 100]",
		"walking_heart_rate_average":       "(0, 250]",
		"heart_rate_recovery_one_minute":   "[0, 200]",
		"blood_pressure_systolic":          "(0, 300]",
		"blood_pressure_diastolic":         "(0, 200]",
		"blood_glucose":                    "(0, 1000]",
		"basal_energy":                     "[0, 20000]",
		"apple_exercise_time":              "[0, 1440]",
		"apple_stand_time":                 "[0, 1440]",
		"flights_climbed":                  "[0, 10000]",
		"distance_walking_running":         "[0, 500]",
		"time_in_daylight":                 "[0, 1440]",
		"body_fat_percentage":              "[0, 100]",
		"body_mass_index":                  "(0, 100]",
		"lean_body_mass":                   "(0, 500]",
		"height":                           "(0, 300]",
		"walking_speed":                    "(0, 30]",
		"walking_step_length":              "(0, 200]",
		"walking_asymmetry":                "[0, 100]",
		"walking_double_support":           "[0, 100]",
		"apple_walking_steadiness":         "[0, 100]",
	}
	for _, name := range Names() {
		m, _ := Lookup(name)
		open := "["
		if m.Limits.ExcludeMin {
			open = "("
		}
		got := fmt.Sprintf("%s%g, %g]", open, m.Limits.Min, m.Limits.Max)
		if got != want[name] {
			t.Errorf("%s: limits %s, table says %q", name, got, want[name])
		}
		if m.Limits.Max <= m.Limits.Min {
			t.Errorf("%s: Max must be above Min", name)
		}
		delete(want, name)
	}
	for name := range want {
		t.Errorf("table lists %s, which is not in the registry", name)
	}
}

// Registry short names replace the old stats label map.
func TestShortNames(t *testing.T) {
	for name, want := range map[string]string{"heart_rate": "Avg heart rate", "resting_heart_rate": "Resting HR", "hrv_sdnn": "HRV"} {
		if m, _ := Lookup(name); m.Short != want {
			t.Errorf("%s Short = %q, want %q", name, m.Short, want)
		}
	}
}
