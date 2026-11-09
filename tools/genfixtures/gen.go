package main

import (
	"bufio"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// dateLayout is the date format Apple uses everywhere in export.xml.
const dateLayout = "2006-01-02 15:04:05 -0700"

const (
	homeOffset = 1 * 3600  // +0100
	tripOffset = -5 * 3600 // -0500
)

// profile describes how big and how dense a dataset is.
type profile struct {
	start        time.Time // first day (only the date matters)
	days         int
	hrEvery      int   // minutes between heart-rate samples
	stepEvery    int   // minutes between step / energy buckets
	tripDays     []int // day numbers (0-based) spent in the trip timezone
	malformFrom  int   // a malformed record is emitted when day%malformEvery == malformFrom
	malformEvery int
	// sparse mimics a simple non-Apple-Watch device (a fitness band and a
	// smart scale): only steps, heart rate and weight, no sleep, no workouts,
	// no malformed records.
	sparse bool
}

var profiles = map[string]profile{
	"small": {
		start: time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC), days: 14,
		hrEvery: 60, stepEvery: 60,
		tripDays:     []int{5, 6, 7, 8, 9},
		malformEvery: 5, malformFrom: 2, // days 2, 7, 12
	},
	"large": {
		start: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), days: 4 * 365,
		hrEvery: 1, stepEvery: 5,
		tripDays:     tripsEveryYear(4),
		malformEvery: 30, malformFrom: 2,
	},
	"sparse": {
		start: time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC), days: 28,
		hrEvery: 30, stepEvery: 60,
		sparse: true,
	},
}

// tripsEveryYear returns two weeks in July of each year as trip days.
func tripsEveryYear(years int) []int {
	var d []int
	for y := 0; y < years; y++ {
		for i := 0; i < 14; i++ {
			d = append(d, y*365+190+i)
		}
	}
	return d
}

// source is the device that "recorded" a sample. Real exports use a curly
// apostrophe (U+2019) in device names, so we do too: it tests UTF-8 handling.
type source struct{ name, version string }

var (
	iphone = source{"Alex’s iPhone", "17.2"}
	watch  = source{"Alex’s Apple Watch", "10.2"}
	band   = source{"FitBand 3", "2.1"} // the non-Apple device of the sparse profile
	scale  = source{"SmartScale", "1.0"}
)

type gen struct {
	w    *bufio.Writer
	prof profile
	rng  *rand.Rand
	// vrng feeds only the newer vitals, so adding metrics never changes the
	// values of the older ones (the committed small fixture stays stable).
	vrng *rand.Rand
	trip map[int]bool
}

func newGen(w *bufio.Writer, prof profile, seed int64) *gen {
	g := &gen{w: w, prof: prof, rng: rand.New(rand.NewSource(seed)), vrng: rand.New(rand.NewSource(seed + 7919)), trip: map[int]bool{}}
	for _, d := range prof.tripDays {
		g.trip[d] = true
	}
	return g
}

func (g *gen) writeAll() {
	end := g.prof.start.AddDate(0, 0, g.prof.days)
	g.writeHeader(end)
	for day := 0; day < g.prof.days; day++ {
		g.writeDay(day)
	}
	g.w.WriteString("</HealthData>\n")
}

// at builds a local wall-clock time for a day, using that day's UTC offset.
// Offsets change between days, which is how the mid-dataset timezone shift appears.
func (g *gen) at(day, hour, min int) time.Time {
	off := homeOffset
	if g.trip[day] {
		off = tripOffset
	}
	d := g.prof.start.AddDate(0, 0, day)
	return time.Date(d.Year(), d.Month(), d.Day(), hour, min, 0, 0, time.FixedZone("", off))
}

func (g *gen) writeDay(day int) {
	if g.prof.sparse {
		g.writeSparseDay(day)
		return
	}
	// Sample times are bucketed through the day; hours 7-22 count as "awake".
	for m := 0; m < 24*60; m += g.prof.hrEvery {
		start := g.at(day, 0, m)
		asleep := m < 7*60 || m >= 23*60
		bpm := 58 + g.rng.Intn(8)
		if !asleep {
			bpm = 65 + g.rng.Intn(35)
		}
		g.record("HKQuantityTypeIdentifierHeartRate", watch, "count/min", start, start.Add(time.Minute), fmt.Sprint(bpm), "")
	}
	for m := 7 * 60; m < 23*60; m += g.prof.stepEvery {
		start, end := g.at(day, 0, m), g.at(day, 0, m+g.prof.stepEvery)
		steps := g.rng.Intn(40 * g.prof.stepEvery)
		// Same period, two sources: the importer must not double-count these.
		g.record("HKQuantityTypeIdentifierStepCount", watch, "count", start, end, fmt.Sprint(steps), "")
		g.record("HKQuantityTypeIdentifierStepCount", iphone, "count", start, end, fmt.Sprint(steps*9/10), "")
		kcal := float64(steps) * 0.04
		g.record("HKQuantityTypeIdentifierActiveEnergyBurned", watch, "kcal", start, end, fmt.Sprintf("%.3f", kcal), "")
	}

	// Once-a-day readings.
	hrv := g.at(day, 6, 40)
	g.record("HKQuantityTypeIdentifierHeartRateVariabilitySDNN", watch, "ms", hrv, hrv, fmt.Sprintf("%.2f", 35+g.rng.Float64()*30),
		`<HeartRateVariabilityMetadataList><InstantaneousBeatsPerMinute bpm="62" time="6:40:01.00"/></HeartRateVariabilityMetadataList>`)
	rest := g.at(day, 12, 0)
	restLine := func() {
		g.record("HKQuantityTypeIdentifierRestingHeartRate", watch, "count/min", rest, rest, fmt.Sprint(54+day%7), "")
	}
	restLine()
	if day%5 == 3 {
		restLine() // byte-identical repeat: an importer should be idempotent
	}
	if day%3 == 0 {
		w := g.at(day, 7, 5)
		g.record("HKQuantityTypeIdentifierBodyMass", iphone, "kg", w, w, fmt.Sprintf("%.1f", 78+g.rng.Float64()*2),
			`<MetadataEntry key="HKWasUserEntered" value="1"/>`)
	}

	g.writeVitals(day)
	g.writeSleep(day)
	g.writeWorkout(day)
	g.writeMalformed(day)
}

// writeSparseDay is a whole day for the sparse profile: heart rate and steps
// from one band, and a weigh-in every third day from a scale. Nothing else.
func (g *gen) writeSparseDay(day int) {
	for m := 0; m < 24*60; m += g.prof.hrEvery {
		start := g.at(day, 0, m)
		bpm := 60 + g.rng.Intn(10)
		if m >= 7*60 && m < 23*60 {
			bpm = 68 + g.rng.Intn(40)
		}
		g.record("HKQuantityTypeIdentifierHeartRate", band, "count/min", start, start.Add(time.Minute), fmt.Sprint(bpm), "")
	}
	for m := 7 * 60; m < 23*60; m += g.prof.stepEvery {
		start, end := g.at(day, 0, m), g.at(day, 0, m+g.prof.stepEvery)
		g.record("HKQuantityTypeIdentifierStepCount", band, "count", start, end, fmt.Sprint(g.rng.Intn(40*g.prof.stepEvery)), "")
	}
	if day%3 == 0 {
		w := g.at(day, 7, 30)
		g.record("HKQuantityTypeIdentifierBodyMass", scale, "kg", w, w, fmt.Sprintf("%.1f", 82+g.rng.Float64()*2), "")
	}
}

// writeVitals emits the once-a-day (or occasional) readings a recent Apple
// Watch adds beyond heart rate and steps. They also exercise Apple's unit
// quirks: percentages as 0..1 fractions under "%", VO2 max with a middle dot,
// speed as "km/hr", BMI as a plain "count".
func (g *gen) writeVitals(day int) {
	const id = "HKQuantityTypeIdentifier"
	night := g.at(day, 5, 30)
	g.record(id+"RespiratoryRate", watch, "count/min", night, night, fmt.Sprintf("%.1f", 13+g.vrng.Float64()*3), "")
	g.record(id+"AppleSleepingWristTemperature", watch, "degC", night, night, fmt.Sprintf("%.2f", 35.2+g.vrng.Float64()*1.2), "")
	for _, h := range []int{3, 14} { // blood oxygen is a fraction, not a percent
		t := g.at(day, h, 10)
		g.record(id+"OxygenSaturation", watch, "%", t, t, fmt.Sprintf("%.2f", 0.95+g.vrng.Float64()*0.04), "")
	}

	noon := g.at(day, 12, 30)
	g.record(id+"WalkingHeartRateAverage", watch, "count/min", noon, noon, fmt.Sprint(88+g.vrng.Intn(12)), "")
	g.record(id+"WalkingSpeed", iphone, "km/hr", noon, noon, fmt.Sprintf("%.2f", 4.8+g.vrng.Float64()*1.0), "")
	g.record(id+"WalkingStepLength", iphone, "cm", noon, noon, fmt.Sprintf("%.1f", 68+g.vrng.Float64()*6), "")
	g.record(id+"WalkingAsymmetryPercentage", iphone, "%", noon, noon, fmt.Sprintf("%.3f", 0.02+g.vrng.Float64()*0.04), "")
	g.record(id+"WalkingDoubleSupportPercentage", iphone, "%", noon, noon, fmt.Sprintf("%.3f", 0.26+g.vrng.Float64()*0.05), "")
	if day%7 == 0 {
		g.record(id+"AppleWalkingSteadiness", iphone, "%", noon, noon, fmt.Sprintf("%.2f", 0.85+g.vrng.Float64()*0.1), "")
		g.record(id+"VO2Max", watch, "mL/min·kg", noon, noon, fmt.Sprintf("%.1f", 40+g.vrng.Float64()*3), "")
	}
	if day%2 == 0 { // workout days: heart rate recovery after the run
		t := g.at(day, 18, 50)
		g.record(id+"HeartRateRecoveryOneMinute", watch, "count/min", t, t, fmt.Sprint(22+g.vrng.Intn(14)), "")
	}

	// Whole-day totals are written as one record each, like Apple's rings.
	dayStart, dayEnd := g.at(day, 0, 0), g.at(day, 23, 59)
	g.record(id+"AppleExerciseTime", watch, "min", dayStart, dayEnd, fmt.Sprint(15+g.vrng.Intn(45)), "")
	g.record(id+"AppleStandTime", watch, "min", dayStart, dayEnd, fmt.Sprint(300+g.vrng.Intn(240)), "")
	g.record(id+"FlightsClimbed", iphone, "count", dayStart, dayEnd, fmt.Sprint(4+g.vrng.Intn(20)), "")
	g.record(id+"DistanceWalkingRunning", iphone, "km", dayStart, dayEnd, fmt.Sprintf("%.3f", 3+g.vrng.Float64()*7), "")
	g.record(id+"BasalEnergyBurned", watch, "kcal", dayStart, dayEnd, fmt.Sprintf("%.1f", 1650+g.vrng.Float64()*100), "")
	g.record(id+"TimeInDaylight", watch, "min", dayStart, dayEnd, fmt.Sprint(20+g.vrng.Intn(90)), "")

	if day%3 == 0 { // body composition arrives with a weigh-in
		w := g.at(day, 7, 6)
		g.record(id+"BodyFatPercentage", iphone, "%", w, w, fmt.Sprintf("%.3f", 0.18+g.vrng.Float64()*0.02), "")
		g.record(id+"BodyMassIndex", iphone, "count", w, w, fmt.Sprintf("%.1f", 24+g.vrng.Float64()*0.6), "")
		g.record(id+"LeanBodyMass", iphone, "kg", w, w, fmt.Sprintf("%.1f", 63+g.vrng.Float64()*1.5), "")
	}
}

// writeSleep emits a night starting at 23:00 on `day`: an iPhone InBed span
// plus Apple Watch stage records (or, every 7th night, a legacy
// AsleepUnspecified block like pre-iOS-16 exports have).
func (g *gen) writeSleep(day int) {
	const t = "HKCategoryTypeIdentifierSleepAnalysis"
	const p = "HKCategoryValueSleepAnalysis"
	bedStart := g.at(day, 23, 0)
	bedEnd := bedStart.Add(7*time.Hour + time.Duration(g.rng.Intn(60))*time.Minute)
	g.record(t, iphone, "", bedStart, bedEnd, p+"InBed", "")
	if day%7 == 6 {
		g.record(t, iphone, "", bedStart.Add(10*time.Minute), bedEnd, p+"AsleepUnspecified", "")
		return
	}
	// Cycle through stages until we run out of night.
	stages := []struct {
		val string
		min int
	}{{"AsleepCore", 45}, {"AsleepDeep", 25}, {"AsleepCore", 30}, {"AsleepREM", 25}, {"Awake", 4}}
	cur := bedStart.Add(12 * time.Minute)
	for i := 0; cur.Before(bedEnd); i++ {
		s := stages[i%len(stages)]
		next := cur.Add(time.Duration(s.min+g.rng.Intn(10)) * time.Minute)
		if next.After(bedEnd) {
			next = bedEnd
		}
		g.record(t, watch, "", cur, next, p+s.val, "")
		cur = next
	}
}

func (g *gen) writeWorkout(day int) {
	if day%2 == 1 {
		return
	}
	kinds := []struct {
		typ      string
		speedKmh float64
		kcalKm   float64
	}{
		{"HKWorkoutActivityTypeRunning", 10, 65},
		{"HKWorkoutActivityTypeWalking", 5, 45},
		{"HKWorkoutActivityTypeCycling", 20, 30},
	}
	k := kinds[(day/2)%len(kinds)]
	minutes := 25 + g.rng.Float64()*50
	km := k.speedKmh * minutes / 60
	start := g.at(day, 18, 0)
	end := start.Add(time.Duration(minutes * float64(time.Minute)))
	kcal := km * k.kcalKm
	distType := "DistanceWalkingRunning"
	if k.typ == "HKWorkoutActivityTypeCycling" {
		distType = "DistanceCycling"
	}
	stat := func(typ string, sum float64, unit string) string {
		return fmt.Sprintf(`  <WorkoutStatistics type="HKQuantityTypeIdentifier%s" startDate="%s" endDate="%s" sum="%.4f" unit="%s"/>
`, typ, fmtTime(start), fmtTime(end), sum, unit)
	}
	// Two styles, alternating. Old exports carry the totals as attributes of
	// <Workout>; iOS 16+ exports leave them off and use <WorkoutStatistics>
	// children only. Both describe the same numbers.
	totals := ""
	if (day/2)%2 == 0 {
		totals = fmt.Sprintf(` totalDistance="%.4f" totalDistanceUnit="km" totalEnergyBurned="%.4f" totalEnergyBurnedUnit="kcal"`, km, kcal)
	}
	fmt.Fprintf(g.w, ` <Workout workoutActivityType="%s" duration="%.4f" durationUnit="min"%s sourceName="%s" sourceVersion="%s" creationDate="%s" startDate="%s" endDate="%s">
  <MetadataEntry key="HKIndoorWorkout" value="0"/>
%s%s </Workout>
`, k.typ, minutes, totals, watch.name, watch.version, fmtTime(end), fmtTime(start), fmtTime(end),
		stat("ActiveEnergyBurned", kcal, "kcal"), stat(distType, km, "km"))
}

// writeMalformed emits records a robust importer must skip. All are still
// well-formed XML, so a streaming parser reaches them and must decide.
func (g *gen) writeMalformed(day int) {
	if day%g.prof.malformEvery != g.prof.malformFrom {
		return
	}
	s := g.at(day, 14, 0)
	e := s.Add(time.Minute)
	switch (day / g.prof.malformEvery) % 4 {
	case 0: // missing value attribute
		fmt.Fprintf(g.w, ` <Record type="HKQuantityTypeIdentifierHeartRate" sourceName="%s" sourceVersion="%s" unit="count/min" creationDate="%s" startDate="%s" endDate="%s"/>
`, watch.name, watch.version, fmtTime(e), fmtTime(s), fmtTime(e))
	case 1: // impossible date
		fmt.Fprintf(g.w, ` <Record type="HKQuantityTypeIdentifierStepCount" sourceName="%s" sourceVersion="%s" unit="count" creationDate="%s" startDate="2024-13-45 99:99:99 +0100" endDate="%s" value="12"/>
`, iphone.name, iphone.version, fmtTime(e), fmtTime(e))
	case 2: // type this importer has never heard of
		g.record("HKQuantityTypeIdentifierSomethingFromTheFuture", watch, "count", s, e, "1", "")
	case 3: // value that is not a number
		g.record("HKQuantityTypeIdentifierHeartRate", watch, "count/min", s, e, "fast", "")
	}
}

// record writes one <Record>. `children` is raw XML placed inside it.
func (g *gen) record(typ string, src source, unit string, start, end time.Time, value, children string) {
	g.w.WriteString(` <Record type="` + typ + `" sourceName="` + src.name + `" sourceVersion="` + src.version + `"`)
	if unit != "" {
		g.w.WriteString(` unit="` + unit + `"`)
	}
	g.w.WriteString(` creationDate="` + fmtTime(end.Add(2*time.Second)) + `" startDate="` + fmtTime(start) + `" endDate="` + fmtTime(end) + `" value="` + value + `"`)
	if children == "" {
		g.w.WriteString("/>\n")
		return
	}
	g.w.WriteString(">\n  " + children + "\n </Record>\n")
}

func fmtTime(t time.Time) string { return t.Format(dateLayout) }

func (g *gen) writeHeader(end time.Time) {
	exportDate := time.Date(end.Year(), end.Month(), end.Day(), 8, 30, 0, 0, time.FixedZone("", homeOffset))
	g.w.WriteString(xmlHeader)
	g.w.WriteString(strings.Replace(`<HealthData locale="en_US">
 <ExportDate value="DATE"/>
 <Me HKCharacteristicTypeIdentifierDateOfBirth="1990-01-01" HKCharacteristicTypeIdentifierBiologicalSex="HKBiologicalSexNotSet" HKCharacteristicTypeIdentifierBloodType="HKBloodTypeNotSet" HKCharacteristicTypeIdentifierFitzpatrickSkinType="HKFitzpatrickSkinTypeNotSet" HKCharacteristicTypeIdentifierCardioFitnessMedicationsUse="None"/>
`, "DATE", fmtTime(exportDate), 1))
}

// xmlHeader mimics the DOCTYPE Apple writes. The internal DTD matters: a parser
// must tolerate it (Go's encoding/xml reads it as a Directive token).
const xmlHeader = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE HealthData [
<!-- HealthKit Export Version: 14 -->
<!ELEMENT HealthData (ExportDate,Me,(Record|Correlation|Workout|ActivitySummary|ClinicalRecord|Audiogram|VisionPrescription)*)>
<!ATTLIST HealthData
  locale CDATA #REQUIRED
>
<!ELEMENT ExportDate EMPTY>
<!ATTLIST ExportDate
  value CDATA #REQUIRED
>
<!ELEMENT Me EMPTY>
<!ATTLIST Me
  HKCharacteristicTypeIdentifierDateOfBirth         CDATA #REQUIRED
  HKCharacteristicTypeIdentifierBiologicalSex       CDATA #REQUIRED
  HKCharacteristicTypeIdentifierBloodType           CDATA #REQUIRED
  HKCharacteristicTypeIdentifierFitzpatrickSkinType CDATA #REQUIRED
>
<!ELEMENT Record ((MetadataEntry|HeartRateVariabilityMetadataList)*)>
<!ATTLIST Record
  type          CDATA #REQUIRED
  unit          CDATA #IMPLIED
  value         CDATA #IMPLIED
  sourceName    CDATA #REQUIRED
  sourceVersion CDATA #IMPLIED
  device        CDATA #IMPLIED
  creationDate  CDATA #IMPLIED
  startDate     CDATA #REQUIRED
  endDate       CDATA #REQUIRED
>
<!ELEMENT MetadataEntry EMPTY>
<!ATTLIST MetadataEntry
  key   CDATA #REQUIRED
  value CDATA #REQUIRED
>
<!ELEMENT HeartRateVariabilityMetadataList (InstantaneousBeatsPerMinute*)>
<!ELEMENT InstantaneousBeatsPerMinute EMPTY>
<!ATTLIST InstantaneousBeatsPerMinute
  bpm  CDATA #REQUIRED
  time CDATA #REQUIRED
>
<!ELEMENT Workout ((MetadataEntry|WorkoutEvent|WorkoutStatistics)*)>
<!ATTLIST Workout
  workoutActivityType   CDATA #REQUIRED
  duration              CDATA #IMPLIED
  durationUnit          CDATA #IMPLIED
  totalDistance         CDATA #IMPLIED
  totalDistanceUnit     CDATA #IMPLIED
  totalEnergyBurned     CDATA #IMPLIED
  totalEnergyBurnedUnit CDATA #IMPLIED
  sourceName            CDATA #REQUIRED
  sourceVersion         CDATA #IMPLIED
  device                CDATA #IMPLIED
  creationDate          CDATA #IMPLIED
  startDate             CDATA #REQUIRED
  endDate               CDATA #REQUIRED
>
<!ELEMENT WorkoutEvent EMPTY>
<!ELEMENT WorkoutStatistics EMPTY>
<!ATTLIST WorkoutStatistics
  type      CDATA #REQUIRED
  startDate CDATA #REQUIRED
  endDate   CDATA #REQUIRED
  sum       CDATA #IMPLIED
  unit      CDATA #IMPLIED
>
]>
`
