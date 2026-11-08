package importer

import (
	"math"
	"testing"
)

// iOS 16+ and Apple Watch exports often leave totalEnergyBurned/totalDistance
// off the <Workout> tag and put the totals in <WorkoutStatistics> children.
// These tests pin down how the importer reads them.

const wkStart = `workoutActivityType="HKWorkoutActivityTypeRunning" duration="30" durationUnit="min" sourceName="W" ` + qaWhen

// stat builds one <WorkoutStatistics> child.
func stat(typ, sum, unit string) string {
	return `<WorkoutStatistics type="HKQuantityTypeIdentifier` + typ + `" ` + qaWhen + ` sum="` + sum + `" unit="` + unit + `"/>`
}

type wkRow struct {
	dur      float64
	energy   sqlFloat
	distance sqlFloat
}

type sqlFloat struct {
	v     float64
	valid bool
}

func importOneWorkout(t *testing.T, workout string) (wkRow, *Summary) {
	t.Helper()
	conn := newDB(t)
	sum, err := Import(conn, writeFile(t, "x.xml", wrap(workout)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	var r wkRow
	var e, d *float64
	rows, err := conn.Query("SELECT duration_s, energy_kcal, distance_m FROM workouts")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.Scan(&r.dur, &e, &d); err != nil {
			t.Fatal(err)
		}
	}
	if e != nil {
		r.energy = sqlFloat{*e, true}
	}
	if d != nil {
		r.distance = sqlFloat{*d, true}
	}
	return r, sum
}

func near(a sqlFloat, want float64) bool { return a.valid && math.Abs(a.v-want) < 1e-3 }

func TestWorkoutTotalsFromStatistics(t *testing.T) {
	r, sum := importOneWorkout(t, `<Workout `+wkStart+`>
  <MetadataEntry key="HKIndoorWorkout" value="0"/>
  `+stat("ActiveEnergyBurned", "418.4", "kJ")+`
  `+stat("DistanceWalkingRunning", "3.1", "mi")+`
 </Workout>`)
	if sum.Inserted != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	if !near(r.energy, 100) { // 418.4 kJ = 100 kcal
		t.Errorf("energy = %+v, want 100 kcal", r.energy)
	}
	if !near(r.distance, 4988.9664) { // 3.1 mi
		t.Errorf("distance = %+v, want 4988.9664 m", r.distance)
	}
}

func TestWorkoutStatisticsUnitsAndCyclingDistance(t *testing.T) {
	r, _ := importOneWorkout(t, `<Workout `+wkStart+`>`+
		stat("ActiveEnergyBurned", "250", "kcal")+stat("DistanceCycling", "12.5", "km")+`</Workout>`)
	if !near(r.energy, 250) || !near(r.distance, 12500) {
		t.Errorf("got %+v", r)
	}
}

// If the attribute is on the <Workout> it wins, per field, and the other field
// can still come from a child.
func TestWorkoutAttributesWinOverStatistics(t *testing.T) {
	r, _ := importOneWorkout(t, `<Workout `+wkStart+` totalEnergyBurned="300" totalEnergyBurnedUnit="kcal">`+
		stat("ActiveEnergyBurned", "999", "kcal")+stat("DistanceWalkingRunning", "5", "km")+`</Workout>`)
	if !near(r.energy, 300) {
		t.Errorf("energy = %+v, the attribute (300) must win over the child (999)", r.energy)
	}
	if !near(r.distance, 5000) {
		t.Errorf("distance = %+v, want 5000 from the child", r.distance)
	}
}

func TestWorkoutIgnoresOtherStatistics(t *testing.T) {
	// Heart rate stats carry average/min/max, not a total; step count is not a
	// distance. Neither may leak into the totals, and the workout still imports.
	r, sum := importOneWorkout(t, `<Workout `+wkStart+`>
  <WorkoutStatistics type="HKQuantityTypeIdentifierHeartRate" `+qaWhen+` average="140" minimum="90" maximum="170" unit="count/min"/>
  `+stat("StepCount", "4000", "count")+`
  `+stat("DistanceWalkingRunning", "", "km")+`
 </Workout>`)
	if sum.Inserted != 1 || r.energy.valid || r.distance.valid {
		t.Errorf("row = %+v, sum = %+v; want NULL energy and distance", r, sum)
	}
}

func TestWorkoutBadStatisticIsSkippedAndCounted(t *testing.T) {
	for name, child := range map[string]string{
		"not a number": stat("ActiveEnergyBurned", "lots", "kcal"),
		"NaN":          stat("ActiveEnergyBurned", "NaN", "kcal"),
		"unknown unit": stat("DistanceWalkingRunning", "5", "parsecs"),
	} {
		_, sum := importOneWorkout(t, `<Workout `+wkStart+`>`+child+`</Workout>`)
		if sum.Inserted != 0 || sum.Skipped[reasonBadWorkout] != 1 {
			t.Errorf("%s: summary = %+v, want one bad_workout", name, sum)
		}
	}
}

// Reading a workout's children must not swallow what follows it.
func TestRecordsAfterWorkoutChildrenStillImport(t *testing.T) {
	conn := newDB(t)
	sum, err := Import(conn, writeFile(t, "x.xml", wrap(
		`<Workout `+wkStart+`>`+stat("ActiveEnergyBurned", "1", "kcal")+`</Workout>`+oneStepRecord+
			`<Workout `+wkStart+` startDate="2024-12-02 10:00:00 +0100" endDate="2024-12-02 10:30:00 +0100"/>`)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Read != 3 || sum.Inserted != 3 {
		t.Errorf("summary = %+v, want 3 read and 3 inserted", sum)
	}
}

// A truncated workout (file cut inside it) is still a clean error.
func TestTruncatedInsideWorkoutIsAnError(t *testing.T) {
	_, err := Import(newDB(t), writeFile(t, "x.xml", `<HealthData><Workout `+wkStart+`><WorkoutStatistics `), Options{})
	if err == nil {
		t.Error("want an error")
	}
}

func TestStatisticsWorkoutsReimportIdentical(t *testing.T) {
	p := writeFile(t, "x.xml", wrap(`<Workout `+wkStart+`>`+stat("ActiveEnergyBurned", "5", "kJ")+`</Workout>`))
	conn := newDB(t)
	if _, err := Import(conn, p, Options{}); err != nil {
		t.Fatal(err)
	}
	first := dump(t, conn)
	sum, err := Import(conn, p, Options{})
	if err != nil || sum.Inserted != 0 || sum.Duplicates != 1 || dump(t, conn) != first {
		t.Errorf("second import: %+v, %v", sum, err)
	}
}

// Every workout in the small fixture, in either style, has both totals.
func TestSmallFixtureWorkoutsAllHaveTotals(t *testing.T) {
	conn := newDB(t)
	if _, err := Import(conn, smallFixture, Options{}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, conn, "SELECT COUNT(*) FROM workouts WHERE energy_kcal IS NULL OR distance_m IS NULL"); n != 0 {
		t.Errorf("%d fixture workouts have NULL totals", n)
	}
}
