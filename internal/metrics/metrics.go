// Package metrics is the list of health metrics the CLI knows about:
// their stored (snake_case) names, canonical units and friendly aliases.
package metrics

import (
	"fmt"
	"sort"
	"strings"
)

// Metric describes one kind of measurement.
type Metric struct {
	Name string // stored in samples.metric, e.g. "heart_rate"
	Unit string // canonical unit, stored in samples.unit
	// Conversions maps other units a user may type to the factor that
	// turns them into the canonical unit (value_in_canonical = value * factor).
	Conversions map[string]float64
}

// poundsToKg is the exact definition of the avoirdupois pound.
const poundsToKg = 0.45359237

var known = []Metric{
	{Name: "heart_rate", Unit: "count/min"},
	{Name: "resting_heart_rate", Unit: "count/min"},
	{Name: "step_count", Unit: "count"},
	{Name: "hrv_sdnn", Unit: "ms"},
	{Name: "body_mass", Unit: "kg", Conversions: map[string]float64{"lb": poundsToKg, "lbs": poundsToKg}},
	{Name: "active_energy", Unit: "kcal"},
}

// aliases maps friendly names people type to canonical metric names.
var aliases = map[string]string{
	"weight": "body_mass",
	"hr":     "heart_rate",
	"steps":  "step_count",
}

// Lookup finds a metric by canonical name or alias (case-insensitive).
// The error for an unknown name lists every valid choice.
func Lookup(name string) (Metric, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if canonical, ok := aliases[n]; ok {
		n = canonical
	}
	for _, m := range known {
		if m.Name == n {
			return m, nil
		}
	}
	return Metric{}, fmt.Errorf("unknown metric %q\nknown metrics: %s\naliases: %s",
		name, strings.Join(Names(), ", "), aliasList())
}

// Names returns the canonical metric names in registry order.
func Names() []string {
	names := make([]string, 0, len(known))
	for _, m := range known {
		names = append(names, m.Name)
	}
	return names
}

// aliasList renders aliases like "hr=heart_rate, steps=step_count" in a
// stable order (map iteration order is random in Go).
func aliasList() string {
	parts := make([]string, 0, len(aliases))
	for alias, canonical := range aliases {
		parts = append(parts, alias+"="+canonical)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// ToCanonical converts value given in unit into the metric's canonical unit.
// An empty unit means "already canonical".
func (m Metric) ToCanonical(value float64, unit string) (float64, error) {
	u := strings.ToLower(strings.TrimSpace(unit))
	if u == "" || u == strings.ToLower(m.Unit) {
		return value, nil
	}
	if factor, ok := m.Conversions[u]; ok {
		return value * factor, nil
	}
	accepted := []string{m.Unit}
	for other := range m.Conversions {
		accepted = append(accepted, other)
	}
	sort.Strings(accepted[1:])
	return 0, fmt.Errorf("unit %q is not supported for %s (use: %s)", unit, m.Name, strings.Join(accepted, ", "))
}
