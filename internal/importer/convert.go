package importer

import (
	"math"
	"strings"
	"time"

	"github.com/nmutto/health/internal/metrics"
)

// sleepType is Apple's identifier for sleep records (not a numeric metric).
const sleepType = "HKCategoryTypeIdentifierSleepAnalysis"

// sleepStages maps Apple's sleep values to our stage names.
var sleepStages = map[string]string{
	"HKCategoryValueSleepAnalysisInBed":             "in_bed",
	"HKCategoryValueSleepAnalysisAsleepCore":        "core",
	"HKCategoryValueSleepAnalysisAsleepDeep":        "deep",
	"HKCategoryValueSleepAnalysisAsleepREM":         "rem",
	"HKCategoryValueSleepAnalysisAwake":             "awake",
	"HKCategoryValueSleepAnalysisAsleepUnspecified": "asleep_unspecified",
}

// Row types: exactly the columns we insert.
type sampleData struct {
	metric, unit, source string
	start, end           int64
	value                float64
	tz                   int
}

type sleepData struct {
	stage, source string
	start, end    int64
	tz            int
}

type workoutData struct {
	activity, source string
	start, end       int64
	duration         float64
	energy, distance *float64 // nil = not recorded (stored as NULL)
	tz               int
}

// appleTime parses "2024-12-01 18:00:00 +0100" into UTC unix seconds plus the
// record's own offset in seconds east of UTC. The offset is part of the text,
// so we never guess the timezone.
func appleTime(s string) (ts int64, offset int, ok bool) {
	t, err := time.Parse("2006-01-02 15:04:05 -0700", s)
	if err != nil {
		return 0, 0, false
	}
	_, offset = t.Zone()
	return t.Unix(), offset, true
}

// interval parses startDate/endDate and checks the end is not before the start.
func interval(a map[string]string) (start, end int64, tz int, ok bool) {
	start, tz, ok1 := appleTime(a["startDate"])
	end, _, ok2 := appleTime(a["endDate"])
	if !ok1 || !ok2 || end < start {
		return 0, 0, 0, false
	}
	return start, end, tz, true
}

// round4 drops floating-point noise from unit conversions (same as `health log`).
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

// sampleRow converts a numeric <Record>. A non-empty reason means "skip it".
func sampleRow(a map[string]string) (sampleData, string) {
	m, known := metrics.FromApple(a["type"])
	if !known {
		return sampleData{}, reasonUnknownType
	}
	if a["value"] == "" {
		return sampleData{}, reasonMissingValue
	}
	raw, ok := number(a["value"])
	if !ok {
		return sampleData{}, reasonBadValue
	}
	value, err := m.ToCanonical(raw, a["unit"])
	if err != nil {
		return sampleData{}, reasonBadUnit
	}
	start, end, tz, ok := interval(a)
	if !ok {
		return sampleData{}, reasonBadDate
	}
	return sampleData{
		metric: m.Name, unit: m.Unit, source: a["sourceName"],
		start: start, end: end, value: round4(value), tz: tz,
	}, ""
}

// sleepRow converts a sleep <Record>.
func sleepRow(a map[string]string) (sleepData, string) {
	stage, ok := sleepStages[a["value"]]
	if !ok {
		if a["value"] == "" {
			return sleepData{}, reasonMissingValue
		}
		return sleepData{}, reasonBadSleep
	}
	start, end, tz, ok := interval(a)
	if !ok {
		return sleepData{}, reasonBadDate
	}
	return sleepData{stage: stage, source: a["sourceName"], start: start, end: end, tz: tz}, ""
}

// Factors that turn a workout unit into our fixed units (s, kcal, m).
var (
	toSeconds = map[string]float64{"s": 1, "sec": 1, "min": 60, "hr": 3600, "h": 3600}
	toMetres  = map[string]float64{"m": 1, "km": 1000, "mi": 1609.344, "ft": 0.3048}
	toKcal    = map[string]float64{"kcal": 1, "cal": 1, "kj": 1 / 4.184}
)

// optional converts an optional numeric attribute. present is false when the
// attribute is absent; ok is false when it is there but unusable.
func optional(a map[string]string, key, unitKey string, factors map[string]float64) (v *float64, ok bool) {
	if a[key] == "" {
		return nil, true
	}
	raw, good := number(a[key])
	f, known := factors[strings.ToLower(a[unitKey])]
	if !good || !known {
		return nil, false
	}
	out := round4(raw * f)
	return &out, true
}

// workoutRow converts a <Workout> element's own attributes. Child elements
// (events, statistics) are ignored: the totals are already on the element.
func workoutRow(a map[string]string) (workoutData, string) {
	activity := strings.TrimPrefix(a["workoutActivityType"], "HKWorkoutActivityType")
	if activity == "" {
		return workoutData{}, reasonBadWorkout
	}
	start, end, tz, ok := interval(a)
	if !ok {
		return workoutData{}, reasonBadDate
	}

	duration := float64(end - start) // fall back to the interval length
	if a["duration"] != "" {
		d, ok := optional(a, "duration", "durationUnit", toSeconds)
		if !ok {
			return workoutData{}, reasonBadWorkout
		}
		duration = *d
	}
	energy, ok1 := optional(a, "totalEnergyBurned", "totalEnergyBurnedUnit", toKcal)
	distance, ok2 := optional(a, "totalDistance", "totalDistanceUnit", toMetres)
	if !ok1 || !ok2 {
		return workoutData{}, reasonBadWorkout
	}
	return workoutData{
		activity: snake(activity), source: a["sourceName"],
		start: start, end: end, duration: duration,
		energy: energy, distance: distance, tz: tz,
	}, ""
}

// snake turns "HighIntensityIntervalTraining" into "high_intensity_interval_training".
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
