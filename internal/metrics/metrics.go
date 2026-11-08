// Package metrics is the list of health metrics the CLI knows about:
// their stored (snake_case) names, canonical units and friendly aliases.
package metrics

import (
	"fmt"
	"sort"
	"strings"
)

// Aggregation says how many samples of one day become one daily value.
type Aggregation string

const (
	// Sum adds the samples up. Right for amounts that accumulate over a day,
	// such as steps or energy: 300 steps + 500 steps = 800 steps that day.
	Sum Aggregation = "sum"
	// Avg takes the mean. Right for rates sampled many times a day, such as
	// heart rate: adding up heart rates would mean nothing.
	Avg Aggregation = "avg"
	// Latest takes the sample that started last that day. Right for a point
	// reading where the newest one replaces the older ones, such as a weigh-in.
	Latest Aggregation = "latest"
)

// Metric describes one kind of measurement.
type Metric struct {
	Name string // stored in samples.metric, e.g. "heart_rate"
	Unit string // canonical unit, stored in samples.unit
	// Daily is how the samples of one local day become a single value.
	// See docs/design.md ("Read side") for why each metric uses its kind.
	Daily Aggregation
	// Conversions maps other units a user may type to the factor that
	// turns them into the canonical unit (value_in_canonical = value * factor).
	Conversions map[string]float64
}

// poundsToKg is the exact definition of the avoirdupois pound.
const poundsToKg = 0.45359237

// kjToKcal converts kilojoules to (thermochemical) kilocalories.
const kjToKcal = 1 / 4.184

// Heart rates are stored as "bpm". Apple calls the same unit "count/min", so
// it is accepted as an input unit with factor 1.
//
// resting_heart_rate is Avg: Apple usually writes one value per day, but it
// can write a revised estimate later; the mean of a day's estimates is stable
// and equals the single value on normal days. body_mass is Latest: averaging
// a morning and an evening weigh-in gives a number nobody saw on the scale.
var known = []Metric{
	{Name: "heart_rate", Unit: "bpm", Daily: Avg, Conversions: map[string]float64{"count/min": 1}},
	{Name: "resting_heart_rate", Unit: "bpm", Daily: Avg, Conversions: map[string]float64{"count/min": 1}},
	{Name: "step_count", Unit: "count", Daily: Sum},
	{Name: "hrv_sdnn", Unit: "ms", Daily: Avg, Conversions: map[string]float64{"s": 1000}},
	{Name: "body_mass", Unit: "kg", Daily: Latest, Conversions: map[string]float64{"lb": poundsToKg, "lbs": poundsToKg, "g": 0.001}},
	{Name: "active_energy", Unit: "kcal", Daily: Sum, Conversions: map[string]float64{"kj": kjToKcal}},
}

// appleTypes maps Apple's HealthKit identifiers to our metric names. Anything
// not listed here is skipped by the importer as "unknown_type".
var appleTypes = map[string]string{
	"HKQuantityTypeIdentifierHeartRate":                "heart_rate",
	"HKQuantityTypeIdentifierRestingHeartRate":         "resting_heart_rate",
	"HKQuantityTypeIdentifierStepCount":                "step_count",
	"HKQuantityTypeIdentifierHeartRateVariabilitySDNN": "hrv_sdnn",
	"HKQuantityTypeIdentifierBodyMass":                 "body_mass",
	"HKQuantityTypeIdentifierActiveEnergyBurned":       "active_energy",
}

// FromApple returns the metric for an Apple type identifier, if we track it.
func FromApple(identifier string) (Metric, bool) {
	name, ok := appleTypes[identifier]
	if !ok {
		return Metric{}, false
	}
	m, err := Lookup(name)
	return m, err == nil
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
