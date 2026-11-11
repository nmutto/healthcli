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

// Category groups metrics for display (the overview shows one section each).
type Category string

const (
	Vitals   Category = "vitals"
	Activity Category = "activity"
	Body     Category = "body"
	Mobility Category = "mobility"
	Other    Category = "other"
)

// Polarity says which direction of change is generally healthy.
//
// It is a general-population guide used only to colour trends green or red. It
// is NOT medical advice: what is healthy for one person (an athlete's very low
// resting heart rate, someone gaining weight on purpose) may differ, and
// Neutral exists precisely for the metrics where "up" or "down" is not good or
// bad by itself.
type Polarity string

const (
	HigherBetter Polarity = "higher_better"
	LowerBetter  Polarity = "lower_better"
	Neutral      Polarity = "neutral"
	// InRange is for a metric that has no good direction but a healthy band:
	// what matters is whether the level stays inside Metric.Range, not whether
	// it went up or down. A move inside the band is not judged; leaving it is
	// worse and coming back is better (see stats.RangeVerdict).
	InRange Polarity = "in_range"
)

// Valid reports whether p is one of the four known polarities.
func (p Polarity) Valid() bool {
	return p == HigherBetter || p == LowerBetter || p == Neutral || p == InRange
}

// Metric describes one kind of measurement.
// Colour families for heatmaps and trend lines. The CLI turns them into ramps.
// They follow the Apple Health app categories: Heart is red, Respiratory cyan,
// Body Measurements purple (body mass deep violet). Mobility, activity and
// everything else stay orange (a Metric with an empty Hue); sleep and
// workouts set their own hue.
const (
	HueOrange = "orange" // steps and other activity (the default)
	HueGreen  = "green"  // workouts
	HueBlue   = "blue"   // sleep
	HueRed    = "red"    // heart rate metrics
	HueCyan   = "cyan"   // respiratory rate
	HuePurple = "purple" // wrist temperature
	HueViolet = "violet" // body mass
)

type Metric struct {
	Name     string   // stored in samples.metric, e.g. "heart_rate"
	Label    string   // display name, e.g. "Respiratory rate"
	Short    string   // shorter name for narrow tables; "" = same as Label
	Category Category // which overview section it belongs in
	// Hue is the colour family of the metric's heatmaps and trend line (one
	// of the Hue constants). "" means orange.
	Hue  string
	Unit string // canonical unit, stored in samples.unit
	// Limits is the plausibility rule for one stored value (canonical unit).
	// `log` and the Apple importer both call Plausible, so this is the only
	// place that decides what a real reading looks like.
	Limits Limits
	// Decimals is how many digits after the point the CLI prints for this
	// metric (0 for most; 1 for weight, temperature, rates like 16.2/min).
	// It is display only: stored values keep full precision.
	Decimals int
	// MinDelta, when above 0, is the smallest absolute change from the usual
	// that counts as a real change for this metric (in its unit). Otherwise
	// the overview wants a 2% relative change. It keeps tiny wobbles in
	// narrow-range metrics from being called "better" or "worse".
	MinDelta float64
	// Polarity is the general-population guide described on the type.
	Polarity Polarity
	// Range, when set, is a reference limit for the LEVEL of the metric (see
	// Range). Nil for most metrics: only limits with a solid, simple anchor
	// are listed.
	Range *Range
	// Daily is how the samples of one local day become a single value.
	// See docs/design.md ("Read side") for why each metric uses its kind.
	Daily Aggregation
	// Conversions maps other units a user may type to the factor that
	// turns them into the canonical unit (value_in_canonical = value * factor).
	Conversions map[string]float64
	// AppleConversions are extra factors used ONLY when importing an Apple
	// export, checked before Conversions. They exist for Apple quirks that
	// would be wrong for a person typing a value: Apple writes percentages as
	// fractions (unit "%", value 0.97), but `log spo2 97 %` means 97 percent.
	AppleConversions map[string]float64
}

// Limits bounds the values one metric may hold. Min and Max are both allowed,
// except that ExcludeMin makes Min itself an impossible reading (a heart rate
// of 0). A Min below zero means negative values are allowed. Every metric
// sets both bounds, so there is no hidden default cap.
type Limits struct {
	Min, Max   float64
	ExcludeMin bool
}

// upTo: 0 is a real reading (zero steps), negative is not.
func upTo(max float64) Limits { return Limits{Min: 0, Max: max} }

// above: 0 is not a real reading (a weight, a heart rate), only more than 0.
func above(min, max float64) Limits { return Limits{Min: min, Max: max, ExcludeMin: true} }

// between: both ends are real readings, and Min may be negative.
func between(min, max float64) Limits { return Limits{Min: min, Max: max} }

// Range is a general-population reference limit for the level of a metric, in
// the metric's unit. It judges a level against a population, where a verdict
// judges a change against the person's own usual, so it never colours a row:
// it only adds a plain note when the latest value is outside it. A zero bound
// means no limit on that side (a resting heart rate below 60 is normal in
// trained people, so that metric only has High). It is a general guide, not
// medical advice, and wearable readings are estimates.
type Range struct {
	Low, High float64
	Source    string // where the limit comes from, for docs and JSON
	// Basis says in the note what the limit is, in a few words ("the top of
	// Apple's typical walking range"). Empty: "the usual adult limit".
	Basis string
}

// poundsToKg is the exact definition of the avoirdupois pound.
const poundsToKg = 0.45359237

// kjToKcal converts kilojoules to (thermochemical) kilocalories.
const kjToKcal = 1 / 4.184

// energyUnits: kilojoules, plus Apple's "Cal" (the food Calorie), which is a
// kilocalorie. Matching is case-insensitive, like the workouts' toKcal.
var energyUnits = map[string]float64{"kj": kjToKcal, "cal": 1, "kcal": 1}

// perMin lets "count/min" (Apple's spelling of a rate per minute) be read as
// the canonical unit with factor 1.
var perMin = map[string]float64{"count/min": 1}

// appleFraction: Apple stores percentages as a 0..1 fraction under the unit "%".
var appleFraction = map[string]float64{"%": 100}

// vo2Spellings are the ways exports and users write mL/(kg*min). Matching is
// case-insensitive, so "mL/min·kg" is stored here lowercased.
var vo2Spellings = map[string]float64{
	"ml/min·kg": 1, "ml/min*kg": 1, "ml/(kg·min)": 1, "ml/(kg*min)": 1, "ml/min/kg": 1,
}

// toMinutes converts the time units Apple may use for minute-based metrics.
var toMinutes = map[string]float64{"s": 1.0 / 60, "hr": 60, "h": 60}

// Heart rates are stored as "bpm". Apple calls the same unit "count/min", so
// it is accepted as an input unit with factor 1.
//
// resting_heart_rate is Avg: Apple usually writes one value per day, but it
// can write a revised estimate later; the mean of a day's estimates is stable
// and equals the single value on normal days. body_mass is Latest: averaging
// a morning and an evening weigh-in gives a number nobody saw on the scale.
//
// Polarity choices (general guide, see Polarity): higher is better for HRV,
// VO2 max, SpO2, activity amounts, walking speed and steadiness; lower is
// better for resting and walking heart rate, blood pressure, walking
// asymmetry and double support time. Respiratory rate is Neutral: a lower
// resting rate is usually fine, but a rise matters more than the direction.
var known = []Metric{
	// The original six, in their original order (the overview lists them first).
	{Name: "heart_rate", Label: "Heart rate", Short: "Avg heart rate", Category: Vitals, Hue: HueRed, Unit: "bpm", Limits: above(0, 300), Polarity: Neutral, Daily: Avg, Conversions: perMin},
	{Name: "resting_heart_rate", Label: "Resting heart rate", Short: "Resting HR", Category: Vitals, Hue: HueRed, Unit: "bpm", Limits: above(0, 250), Polarity: LowerBetter, Range: &Range{High: 100, Source: "adult resting heart rate, 60-100 bpm"}, Daily: Avg, Conversions: perMin},
	{Name: "step_count", Label: "Steps", Category: Activity, Unit: "count", Limits: upTo(1e6), Polarity: HigherBetter, Daily: Sum},
	{Name: "hrv_sdnn", Label: "Heart rate variability", Short: "HRV", Category: Vitals, Hue: HueRed, Unit: "ms", Limits: above(0, 2000), Polarity: HigherBetter, Daily: Avg, Conversions: map[string]float64{"s": 1000}},
	{Name: "body_mass", Label: "Weight", Category: Body, Hue: HueViolet, Unit: "kg", Limits: above(0, 500), Decimals: 1, Polarity: Neutral, Daily: Latest, Conversions: map[string]float64{"lb": poundsToKg, "lbs": poundsToKg, "g": 0.001}},
	{Name: "active_energy", Label: "Active energy", Category: Activity, Unit: "kcal", Limits: upTo(20000), Polarity: HigherBetter, Daily: Sum, Conversions: energyUnits},

	// Newer metrics, grouped by category.
	// Vitals
	{Name: "respiratory_rate", Label: "Respiratory rate", Category: Vitals, Hue: HueCyan, Unit: "breaths/min", Limits: above(0, 100), Decimals: 1, MinDelta: 0.5, Polarity: Neutral, Range: &Range{High: 20, Source: "adult resting respiratory rate, 12-20 breaths/min"}, Daily: Avg, Conversions: perMin},
	{Name: "apple_sleeping_wrist_temperature", Label: "Wrist temperature", Category: Vitals, Hue: HuePurple, Unit: "degC", Limits: between(-15, 45), Decimals: 1, MinDelta: 0.2, Polarity: Neutral, Daily: Avg, Conversions: map[string]float64{"c": 1, "°c": 1}},
	{Name: "oxygen_saturation", Label: "Blood oxygen", Category: Vitals, Hue: HueCyan, Unit: "percent", Limits: between(50, 100), MinDelta: 1, Polarity: HigherBetter, Range: &Range{Low: 95, Source: "typical healthy adult reading, 95% and above"}, Daily: Avg, Conversions: map[string]float64{"%": 1}, AppleConversions: appleFraction},
	{Name: "vo2_max", Label: "VO2 max", Category: Vitals, Hue: HueRed, Unit: "mL/kg/min", Limits: above(0, 100), Decimals: 1, Polarity: HigherBetter, Daily: Latest, Conversions: vo2Spellings},
	{Name: "walking_heart_rate_average", Label: "Walking heart rate", Category: Vitals, Hue: HueRed, Unit: "bpm", Limits: above(0, 250), Polarity: LowerBetter, Daily: Avg, Conversions: perMin},
	{Name: "heart_rate_recovery_one_minute", Label: "Heart rate recovery", Category: Vitals, Hue: HueRed, Unit: "bpm", Limits: upTo(200), Polarity: HigherBetter, Daily: Avg, Conversions: perMin},
	{Name: "blood_pressure_systolic", Label: "Systolic blood pressure", Category: Vitals, Hue: HueRed, Unit: "mmHg", Limits: above(0, 300), Polarity: LowerBetter, Daily: Avg},
	{Name: "blood_pressure_diastolic", Label: "Diastolic blood pressure", Category: Vitals, Hue: HueRed, Unit: "mmHg", Limits: above(0, 200), Polarity: LowerBetter, Daily: Avg},
	// Glucose has a healthy range, not a direction, hence Neutral. Apple's
	// mmol/L unit carries a molar-mass suffix, "mmol<180.15...>/L".
	{Name: "blood_glucose", Label: "Blood glucose", Category: Vitals, Unit: "mg/dL", Limits: above(0, 1000), Polarity: Neutral, Daily: Avg,
		Conversions: map[string]float64{"mmol/l": 18.0156, "mmol<180.1558800000541>/l": 18.0156}},

	// Activity
	{Name: "basal_energy", Label: "Resting energy", Category: Activity, Unit: "kcal", Limits: upTo(20000), Polarity: Neutral, Daily: Sum, Conversions: energyUnits},
	{Name: "apple_exercise_time", Label: "Exercise time", Category: Activity, Unit: "min", Limits: upTo(1440), Polarity: HigherBetter, Daily: Sum, Conversions: toMinutes},
	{Name: "apple_stand_time", Label: "Stand time", Category: Activity, Unit: "min", Limits: upTo(1440), Polarity: HigherBetter, Daily: Sum, Conversions: toMinutes},
	{Name: "flights_climbed", Label: "Flights climbed", Category: Activity, Unit: "count", Limits: upTo(10000), Polarity: HigherBetter, Daily: Sum},
	{Name: "distance_walking_running", Label: "Walking + running distance", Short: "Walk + run distance", Category: Activity, Unit: "km", Limits: upTo(500), Decimals: 1, Polarity: HigherBetter, Daily: Sum,
		Conversions: map[string]float64{"m": 0.001, "mi": 1.609344}},
	{Name: "time_in_daylight", Label: "Time in daylight", Category: Activity, Unit: "min", Limits: upTo(1440), Polarity: HigherBetter, Daily: Sum, Conversions: toMinutes},

	// Body
	{Name: "body_fat_percentage", Label: "Body fat", Category: Body, Hue: HuePurple, Unit: "percent", Limits: upTo(100), MinDelta: 1, Decimals: 1, Polarity: Neutral, Daily: Latest, Conversions: map[string]float64{"%": 1}, AppleConversions: appleFraction},
	// Apple's BMI unit is the dimensionless "count".
	{Name: "body_mass_index", Label: "BMI", Category: Body, Hue: HuePurple, Unit: "kg/m2", Limits: above(0, 100), Decimals: 1, Polarity: Neutral, Daily: Latest, Conversions: map[string]float64{"count": 1}},
	{Name: "lean_body_mass", Label: "Lean body mass", Category: Body, Hue: HuePurple, Unit: "kg", Limits: above(0, 500), Decimals: 1, Polarity: Neutral, Daily: Latest, Conversions: map[string]float64{"lb": poundsToKg, "lbs": poundsToKg, "g": 0.001}},
	{Name: "height", Label: "Height", Category: Body, Hue: HuePurple, Unit: "cm", Limits: above(0, 300), Polarity: Neutral, Daily: Latest, Conversions: map[string]float64{"m": 100, "in": 2.54, "ft": 30.48}},

	// Mobility
	{Name: "walking_speed", Label: "Walking speed", Category: Mobility, Unit: "km/h", Limits: above(0, 30), Decimals: 1, Polarity: HigherBetter, Daily: Avg,
		Conversions: map[string]float64{"km/hr": 1, "mi/hr": 1.609344, "mph": 1.609344, "m/s": 3.6}},
	{Name: "walking_step_length", Label: "Step length", Category: Mobility, Unit: "cm", Limits: above(0, 200), Polarity: HigherBetter, Daily: Avg, Conversions: map[string]float64{"m": 100, "in": 2.54, "ft": 30.48}},
	{Name: "walking_asymmetry", Label: "Walking asymmetry", Category: Mobility, Unit: "percent", Limits: upTo(100), MinDelta: 1, Decimals: 1, Polarity: InRange,
		Range: &Range{High: 35, Source: "Apple white paper 'Measuring Walking Quality Through iPhone Mobility Metrics' (2022): 35% is the iPhone value that best separates asymmetric from symmetric walkers; Apple gives no healthy range", Basis: "the level Apple's research uses to tell asymmetric walking apart"},
		Daily: Avg, Conversions: map[string]float64{"%": 1}, AppleConversions: appleFraction},
	{Name: "walking_double_support", Label: "Double support time", Category: Mobility, Unit: "percent", Limits: upTo(100), MinDelta: 1, Decimals: 1, Polarity: InRange,
		Range: &Range{High: 40, Source: "Apple white paper 'Measuring Walking Quality Through iPhone Mobility Metrics' (2022): typical walking is 20-40%, lower is better", Basis: "the top of Apple's typical walking range"},
		Daily: Avg, Conversions: map[string]float64{"%": 1}, AppleConversions: appleFraction},
	{Name: "apple_walking_steadiness", Label: "Walking steadiness", Category: Mobility, Unit: "percent", Limits: upTo(100), MinDelta: 1, Polarity: HigherBetter, Daily: Avg, Conversions: map[string]float64{"%": 1}, AppleConversions: appleFraction},
}

// appleTypes maps Apple's HealthKit identifiers to our metric names. Anything
// not listed here is skipped by the importer as "unknown_type".
var appleTypes = map[string]string{
	"HKQuantityTypeIdentifierHeartRate":                      "heart_rate",
	"HKQuantityTypeIdentifierRestingHeartRate":               "resting_heart_rate",
	"HKQuantityTypeIdentifierStepCount":                      "step_count",
	"HKQuantityTypeIdentifierHeartRateVariabilitySDNN":       "hrv_sdnn",
	"HKQuantityTypeIdentifierBodyMass":                       "body_mass",
	"HKQuantityTypeIdentifierActiveEnergyBurned":             "active_energy",
	"HKQuantityTypeIdentifierRespiratoryRate":                "respiratory_rate",
	"HKQuantityTypeIdentifierAppleSleepingWristTemperature":  "apple_sleeping_wrist_temperature",
	"HKQuantityTypeIdentifierOxygenSaturation":               "oxygen_saturation",
	"HKQuantityTypeIdentifierVO2Max":                         "vo2_max",
	"HKQuantityTypeIdentifierWalkingHeartRateAverage":        "walking_heart_rate_average",
	"HKQuantityTypeIdentifierHeartRateRecoveryOneMinute":     "heart_rate_recovery_one_minute",
	"HKQuantityTypeIdentifierWalkingSpeed":                   "walking_speed",
	"HKQuantityTypeIdentifierWalkingStepLength":              "walking_step_length",
	"HKQuantityTypeIdentifierWalkingAsymmetryPercentage":     "walking_asymmetry",
	"HKQuantityTypeIdentifierWalkingDoubleSupportPercentage": "walking_double_support",
	"HKQuantityTypeIdentifierAppleWalkingSteadiness":         "apple_walking_steadiness",
	"HKQuantityTypeIdentifierAppleExerciseTime":              "apple_exercise_time",
	"HKQuantityTypeIdentifierAppleStandTime":                 "apple_stand_time",
	"HKQuantityTypeIdentifierFlightsClimbed":                 "flights_climbed",
	"HKQuantityTypeIdentifierDistanceWalkingRunning":         "distance_walking_running",
	"HKQuantityTypeIdentifierBasalEnergyBurned":              "basal_energy",
	"HKQuantityTypeIdentifierBodyFatPercentage":              "body_fat_percentage",
	"HKQuantityTypeIdentifierBodyMassIndex":                  "body_mass_index",
	"HKQuantityTypeIdentifierLeanBodyMass":                   "lean_body_mass",
	"HKQuantityTypeIdentifierHeight":                         "height",
	"HKQuantityTypeIdentifierBloodPressureSystolic":          "blood_pressure_systolic",
	"HKQuantityTypeIdentifierBloodPressureDiastolic":         "blood_pressure_diastolic",
	"HKQuantityTypeIdentifierBloodGlucose":                   "blood_glucose",
	"HKQuantityTypeIdentifierTimeInDaylight":                 "time_in_daylight",
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
	"hrv":    "hrv_sdnn",
	"rhr":    "resting_heart_rate",
	"steps":  "step_count",
	"rr":     "respiratory_rate",
	"spo2":   "oxygen_saturation",
	"temp":   "apple_sleeping_wrist_temperature",
	"vo2":    "vo2_max",
	"bmi":    "body_mass_index",
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

// FromAppleUnit is ToCanonical for a value read from an Apple export: Apple
// quirks (AppleConversions) are tried first, then the normal conversions.
func (m Metric) FromAppleUnit(value float64, unit string) (float64, error) {
	if factor, ok := m.AppleConversions[strings.ToLower(strings.TrimSpace(unit))]; ok {
		return value * factor, nil
	}
	return m.ToCanonical(value, unit)
}

// ToCanonical converts value given in unit into the metric's canonical unit.
// An empty unit means "already canonical".
func (m Metric) ToCanonical(value float64, unit string) (float64, error) {
	u := strings.ToLower(strings.TrimSpace(unit))
	if u == "" || u == strings.ToLower(m.Unit) {
		return value, nil
	}
	// Fahrenheit is the one conversion that is not a simple factor.
	if m.Unit == "degC" && (u == "degf" || u == "°f" || u == "f") {
		return (value - 32) * 5 / 9, nil
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
