package metrics

import (
	"math"
	"strings"
	"testing"
)

func TestLookupAliases(t *testing.T) {
	for alias, want := range map[string]string{"weight": "body_mass", "HR": "heart_rate", "steps": "step_count", "hrv_sdnn": "hrv_sdnn"} {
		m, err := Lookup(alias)
		if err != nil || m.Name != want {
			t.Errorf("Lookup(%q) = %v, %v; want %s", alias, m.Name, err, want)
		}
	}
}

func TestLookupUnknownListsKnown(t *testing.T) {
	_, err := Lookup("mood")
	if err == nil || !strings.Contains(err.Error(), "body_mass") || !strings.Contains(err.Error(), "weight=body_mass") {
		t.Fatalf("want helpful error, got %v", err)
	}
}

func TestToCanonical(t *testing.T) {
	m, _ := Lookup("weight")
	got, err := m.ToCanonical(150, "lb")
	if err != nil || math.Abs(got-68.0388555) > 1e-6 {
		t.Errorf("150 lb = %v, %v", got, err)
	}
	if got, _ := m.ToCanonical(70, ""); got != 70 {
		t.Errorf("default unit changed value: %v", got)
	}
	if _, err := m.ToCanonical(70, "stone"); err == nil {
		t.Error("want error for unsupported unit")
	}
	hr, _ := Lookup("hr")
	if _, err := hr.ToCanonical(60, "lb"); err == nil {
		t.Error("want error for lb on heart_rate")
	}
}

func TestEveryMetricHasDailyAggregation(t *testing.T) {
	want := map[string]Aggregation{
		"step_count": Sum, "active_energy": Sum,
		"heart_rate": Avg, "hrv_sdnn": Avg, "resting_heart_rate": Avg,
		"body_mass": Latest,
	}
	for _, name := range Names() {
		m, _ := Lookup(name)
		if m.Daily == "" {
			t.Errorf("%s has no Daily aggregation", name)
		}
		if w, ok := want[name]; ok && m.Daily != w {
			t.Errorf("%s: Daily = %s, want %s", name, m.Daily, w)
		}
	}
}

// Table checks that hold for every metric in the registry.
func TestRegistryTable(t *testing.T) {
	categories := map[Category]bool{Vitals: true, Activity: true, Body: true, Mobility: true, Other: true}
	seen := map[string]bool{}
	for _, name := range Names() {
		if seen[name] {
			t.Errorf("duplicate metric name %s", name)
		}
		seen[name] = true
		m, err := Lookup(name)
		if err != nil {
			t.Errorf("Lookup(%q): %v", name, err)
			continue
		}
		if m.Label == "" || m.Unit == "" {
			t.Errorf("%s: label %q and unit %q must both be set", name, m.Label, m.Unit)
		}
		if !categories[m.Category] {
			t.Errorf("%s: invalid category %q", name, m.Category)
		}
		if !m.Polarity.Valid() {
			t.Errorf("%s: invalid polarity %q", name, m.Polarity)
		}
		if name != strings.ToLower(name) || strings.Contains(name, " ") {
			t.Errorf("%s: names must be lower snake_case", name)
		}
	}
}

func TestAliasesResolveToKnownMetrics(t *testing.T) {
	for alias, canonical := range aliases {
		if _, clash := appleTypes[alias]; clash || seenName(alias) {
			t.Errorf("alias %q collides with a canonical name", alias)
		}
		m, err := Lookup(alias)
		if err != nil || m.Name != canonical {
			t.Errorf("alias %q -> %v, %v; want %s", alias, m.Name, err, canonical)
		}
	}
	for _, a := range []string{"rr", "spo2", "temp", "vo2"} {
		if _, ok := aliases[a]; !ok {
			t.Errorf("missing friendly alias %q", a)
		}
	}
}

func seenName(n string) bool {
	for _, name := range Names() {
		if name == n {
			return true
		}
	}
	return false
}

func TestAppleTypesPointAtRegistry(t *testing.T) {
	for id, name := range appleTypes {
		if !strings.HasPrefix(id, "HKQuantityTypeIdentifier") {
			t.Errorf("%s: not a quantity identifier", id)
		}
		if m, ok := FromApple(id); !ok || m.Name != name {
			t.Errorf("FromApple(%s) = %v, %v; want %s", id, m.Name, ok, name)
		}
	}
}

func TestPolarityGuide(t *testing.T) {
	want := map[string]Polarity{
		"hrv_sdnn": HigherBetter, "vo2_max": HigherBetter, "step_count": HigherBetter,
		"oxygen_saturation": HigherBetter, "walking_speed": HigherBetter, "apple_walking_steadiness": HigherBetter,
		"resting_heart_rate": LowerBetter, "walking_heart_rate_average": LowerBetter, "blood_pressure_systolic": LowerBetter,
		"walking_asymmetry": InRange, "walking_double_support": InRange,
		"heart_rate": Neutral, "body_mass": Neutral, "body_mass_index": Neutral, "body_fat_percentage": Neutral,
		"height": Neutral, "apple_sleeping_wrist_temperature": Neutral, "respiratory_rate": Neutral,
	}
	for name, p := range want {
		if m, _ := Lookup(name); m.Polarity != p {
			t.Errorf("%s: polarity %s, want %s", name, m.Polarity, p)
		}
	}
}

func TestAppleUnitQuirks(t *testing.T) {
	cases := []struct {
		metric, unit string
		in, want     float64
	}{
		{"oxygen_saturation", "%", 0.97, 97}, // fraction -> percent
		{"body_fat_percentage", "%", 0.185, 18.5},
		{"walking_double_support", "%", 0.3, 30},
		{"respiratory_rate", "count/min", 14, 14},
		{"vo2_max", "mL/min·kg", 42, 42}, // middle dot
		{"vo2_max", "ml/(kg*min)", 42, 42},
		{"apple_sleeping_wrist_temperature", "degF", 97.7, 36.5},
		{"apple_sleeping_wrist_temperature", "degC", 36.5, 36.5},
		{"walking_speed", "km/hr", 5, 5},
		{"walking_speed", "m/s", 1, 3.6},
		{"walking_step_length", "in", 10, 25.4},
		{"body_mass_index", "count", 24.1, 24.1},
		{"blood_glucose", "mmol<180.1558800000541>/L", 5, 90.078},
		{"apple_exercise_time", "min", 30, 30},
	}
	for _, c := range cases {
		m, err := Lookup(c.metric)
		if err != nil {
			t.Fatal(err)
		}
		got, err := m.FromAppleUnit(c.in, c.unit)
		if err != nil || math.Abs(got-c.want) > 1e-3 {
			t.Errorf("%s %v %q = %v, %v; want %v", c.metric, c.in, c.unit, got, err, c.want)
		}
	}
}

// A person typing "97 %" means 97 percent; only Apple's export uses fractions.
func TestUserPercentIsNotAFraction(t *testing.T) {
	m, _ := Lookup("spo2")
	if got, err := m.ToCanonical(97, "%"); err != nil || got != 97 {
		t.Errorf("97 %% = %v, %v; want 97", got, err)
	}
}

// A reference range has at least one bound, a low bound under the high one,
// and a source, and only metrics where it is a plain note qualify: none of
// the metrics with a range is a polarity-neutral body measure.
func TestRangesAreSane(t *testing.T) {
	for _, name := range Names() {
		m, _ := Lookup(name)
		r := m.Range
		if r == nil {
			continue
		}
		if r.Low <= 0 && r.High <= 0 {
			t.Errorf("%s: range has no bound", name)
		}
		if r.Low > 0 && r.High > 0 && r.Low >= r.High {
			t.Errorf("%s: low %v is not under high %v", name, r.Low, r.High)
		}
		if r.Source == "" {
			t.Errorf("%s: range has no source", name)
		}
	}
	// The ranges that have a source (a one-sided limit each), and no others.
	// Walking steadiness has none yet: Apple does not publish its Low and
	// Very Low cut-offs and none could be verified.
	want := map[string]Range{
		"respiratory_rate":       {High: 20},
		"oxygen_saturation":      {Low: 95},
		"resting_heart_rate":     {High: 100},
		"walking_double_support": {High: 40},
		"walking_asymmetry":      {High: 35},
	}
	for _, name := range Names() {
		m, _ := Lookup(name)
		w, ok := want[name]
		switch {
		case ok && (m.Range == nil || m.Range.Low != w.Low || m.Range.High != w.High):
			t.Errorf("%s: range = %+v, want low %v high %v", name, m.Range, w.Low, w.High)
		case !ok && m.Range != nil:
			t.Errorf("%s has a range that was not approved: %+v", name, m.Range)
		}
	}
}

// An in-range metric is judged by its range, so it must have one.
func TestInRangeMetricsHaveARange(t *testing.T) {
	for _, name := range Names() {
		m, _ := Lookup(name)
		if m.Polarity == InRange && m.Range == nil {
			t.Errorf("%s is in_range but has no Range", name)
		}
	}
}

// A few metrics of each hue; the full comparison with the old map lives in
// stats.TestHueForMatchesOldMap.
func TestRegistryHues(t *testing.T) {
	want := map[string]string{
		"resting_heart_rate": HueRed, "respiratory_rate": HueCyan,
		"height": HuePurple, "body_mass": HueViolet, "step_count": "",
	}
	for name, hue := range want {
		m, _ := Lookup(name)
		if m.Hue != hue {
			t.Errorf("%s: Hue = %q, want %q", name, m.Hue, hue)
		}
	}
}
