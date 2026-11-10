package importer

import (
	"fmt"
	"strings"
	"testing"
)

// Tests in this file started life as failing reproductions of real bugs found
// during QA. Each one says which bug it guards.

const qaWhen = `startDate="2024-12-01 10:00:00 +0100" endDate="2024-12-01 11:00:00 +0100"`

// BUG: a value of "NaN" parsed fine, then SQLite turned the NaN into NULL and
// the NOT NULL constraint aborted the WHOLE import. "Inf" (and numbers that
// overflow to Inf after rounding) were stored and later broke `query --json`.
// One odd record must be skipped, never kill the run or poison the database.
func TestNonFiniteNumbersAreSkippedNotFatal(t *testing.T) {
	p := writeFile(t, "export.xml", wrap(`
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="a" unit="count" `+qaWhen+` value="NaN"/>
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="b" unit="count" `+qaWhen+` value="Inf"/>
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="c" unit="count" `+qaWhen+` value="-inf"/>
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="d" unit="count" `+qaWhen+` value="1e305"/>
<Workout workoutActivityType="HKWorkoutActivityTypeRunning" duration="NaN" durationUnit="min" sourceName="e" `+qaWhen+`/>
<Workout workoutActivityType="HKWorkoutActivityTypeRunning" duration="5" durationUnit="min" totalDistance="Inf" totalDistanceUnit="km" sourceName="f" `+qaWhen+`/>
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="good" unit="count" `+qaWhen+` value="10"/>`))

	conn := newDB(t)
	sum, err := Import(conn, p, Options{})
	if err != nil {
		t.Fatalf("import aborted: %v", err)
	}
	if sum.Inserted != 1 {
		t.Errorf("inserted = %d, want only the good record", sum.Inserted)
	}
	if sum.Skipped[reasonBadValue] != 4 || sum.Skipped[reasonBadWorkout] != 2 {
		t.Errorf("skipped = %v, want 4 bad_value and 2 bad_workout", sum.Skipped)
	}
}

// BUG: iOS 15 and older write sleep as "...SleepAnalysisAsleep" (Apple later
// renamed it AsleepUnspecified). Real, older exports are full of them, and
// they were all thrown away as unknown_sleep_stage.
func TestLegacySleepAsleepValueIsKept(t *testing.T) {
	p := writeFile(t, "export.xml", wrap(`
<Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="Phone" `+qaWhen+` value="HKCategoryValueSleepAnalysisAsleep"/>`))
	conn := newDB(t)
	sum, err := Import(conn, p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Inserted != 1 || sum.SkippedTotal() != 0 {
		t.Fatalf("summary = %+v, want the legacy record imported", sum)
	}
	if n := count(t, conn, "SELECT COUNT(*) FROM sleep_stages WHERE stage = 'asleep_unspecified'"); n != 1 {
		t.Errorf("legacy Asleep should map to asleep_unspecified, got %d rows", n)
	}
}

// BUG: the progress check ran after EVERY start tag, not just after records.
// Once Read hit a multiple of 100000, each child element (MetadataEntry...)
// that followed fired the callback again with the same number.
func TestProgressFiresOncePerMilestone(t *testing.T) {
	var body strings.Builder
	for i := 0; i < progressEvery; i++ {
		fmt.Fprintf(&body, `<Record type="HKQuantityTypeIdentifierStepCount" sourceName="S%d" unit="count" %s value="1"`, i, qaWhen)
		if i == progressEvery-1 {
			// The last record has children: they used to re-trigger progress.
			body.WriteString(`><MetadataEntry key="a" value="1"/><MetadataEntry key="b" value="2"/></Record>`)
		} else {
			body.WriteString(`/>`)
		}
	}
	var calls []int64
	_, err := Import(newDB(t), writeFile(t, "export.xml", wrap(body.String())),
		Options{Progress: func(n int64) { calls = append(calls, n) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != progressEvery {
		t.Errorf("progress calls = %v, want exactly [%d]", calls, progressEvery)
	}
}

// BUG: <Record> elements were imported even before (or without) a
// <HealthData> root. A non-Apple XML file full of <Record> tags was only
// rejected at the very end, after earlier batches had already been committed.
func TestRecordsWithoutHealthDataRootAreRejectedEarly(t *testing.T) {
	var body strings.Builder
	body.WriteString("<NotApple>")
	for i := 0; i < batchSize+10; i++ {
		fmt.Fprintf(&body, `<Record type="HKQuantityTypeIdentifierStepCount" sourceName="S%d" unit="count" %s value="1"/>`, i, qaWhen)
	}
	body.WriteString("</NotApple>")

	conn := newDB(t)
	if _, err := Import(conn, writeFile(t, "x.xml", body.String()), Options{}); err == nil {
		t.Fatal("want an error for a file with no <HealthData>")
	}
	if n := count(t, conn, "SELECT COUNT(*) FROM samples"); n != 0 {
		t.Errorf("%d rows were committed from a non-Apple file, want 0", n)
	}
}

// BUG: Apple's "Cal" is the food Calorie, i.e. a kilocalorie. Energy records
// written in it were skipped as bad_unit because only "kJ" was known.
func TestCalIsKilocalorie(t *testing.T) {
	p := writeFile(t, "export.xml", wrap(`
<Record type="HKQuantityTypeIdentifierActiveEnergyBurned" sourceName="a" unit="Cal" `+qaWhen+` value="120"/>
<Record type="HKQuantityTypeIdentifierBasalEnergyBurned" sourceName="a" unit="kcal" `+qaWhen+` value="80"/>`))
	conn := newDB(t)
	sum, err := Import(conn, p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Inserted != 2 || sum.SkippedTotal() != 0 {
		t.Fatalf("summary = %+v, want both energy records imported", sum)
	}
	if n := count(t, conn, "SELECT COUNT(*) FROM samples WHERE metric = 'active_energy' AND value = 120"); n != 1 {
		t.Errorf("Cal should import unchanged as kcal, got %d rows", n)
	}
}
