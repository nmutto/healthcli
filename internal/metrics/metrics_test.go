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
