package importer

import (
	"testing"
	"time"
)

func TestUnitsAndWorkoutNormalised(t *testing.T) {
	const when = `startDate="2024-12-01 10:00:00 +0100" endDate="2024-12-01 11:00:00 +0100"`
	p := writeFile(t, "export.xml", wrap(`
<Record type="HKQuantityTypeIdentifierBodyMass" sourceName="S" unit="lb" `+when+` value="220.4623"/>
<Record type="HKQuantityTypeIdentifierActiveEnergyBurned" sourceName="S" unit="kJ" `+when+` value="418.4"/>
<Record type="HKQuantityTypeIdentifierHeartRate" sourceName="S" unit="count/min" `+when+` value="70"/>
<Workout workoutActivityType="HKWorkoutActivityTypeHighIntensityIntervalTraining" duration="30" durationUnit="min" totalDistance="5" totalDistanceUnit="km" totalEnergyBurned="300" totalEnergyBurnedUnit="kcal" sourceName="S" `+when+`/>
<Workout workoutActivityType="HKWorkoutActivityTypeYoga" duration="60" durationUnit="min" sourceName="S" `+when+`/>`))
	conn := newDB(t)
	if _, err := Import(conn, p, Options{}); err != nil {
		t.Fatal(err)
	}
	check := func(metric string, wantVal float64, wantUnit string) {
		t.Helper()
		var v float64
		var u string
		if err := conn.QueryRow("SELECT value, unit FROM samples WHERE metric = ?", metric).Scan(&v, &u); err != nil {
			t.Fatal(err)
		}
		if v != wantVal || u != wantUnit {
			t.Errorf("%s = %v %s, want %v %s", metric, v, u, wantVal, wantUnit)
		}
	}
	check("body_mass", 100, "kg")
	check("active_energy", 100, "kcal")
	check("heart_rate", 70, "bpm")

	var act string
	var dur, dist, kcal float64
	err := conn.QueryRow("SELECT activity, duration_s, distance_m, energy_kcal FROM workouts WHERE distance_m IS NOT NULL").Scan(&act, &dur, &dist, &kcal)
	if err != nil {
		t.Fatal(err)
	}
	if act != "high_intensity_interval_training" || dur != 1800 || dist != 5000 || kcal != 300 {
		t.Errorf("workout = %s %v s %v m %v kcal", act, dur, dist, kcal)
	}
	if n := count(t, conn, "SELECT COUNT(*) FROM workouts WHERE distance_m IS NULL AND energy_kcal IS NULL"); n != 1 {
		t.Errorf("workout without totals should store NULLs, got %d such rows", n)
	}
}

func TestImportNewTypesConvertUnits(t *testing.T) {
	conn := newDB(t)
	path := writeFile(t, "export.xml", wrap(
		record("OxygenSaturation", "%", "0.97")+
			record("RespiratoryRate", "count/min", "14.5")+
			record("VO2Max", "mL/min·kg", "41.2")+
			record("AppleSleepingWristTemperature", "degF", "97.7")+
			record("WalkingAsymmetryPercentage", "%", "0.03")+
			record("BodyMassIndex", "count", "24.1")+
			record("WalkingSpeed", "km/hr", "5.1")))
	sum, err := Import(conn, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Inserted != 7 || sum.SkippedTotal() != 0 {
		t.Fatalf("inserted %d, skipped %v; want 7 and none", sum.Inserted, sum.Skipped)
	}
	want := map[string]struct {
		unit  string
		value float64
	}{
		"oxygen_saturation":                {"percent", 97},
		"respiratory_rate":                 {"breaths/min", 14.5},
		"vo2_max":                          {"mL/kg/min", 41.2},
		"apple_sleeping_wrist_temperature": {"degC", 36.5},
		"walking_asymmetry":                {"percent", 3},
		"body_mass_index":                  {"kg/m2", 24.1},
		"walking_speed":                    {"km/h", 5.1},
	}
	for metric, w := range want {
		var unit string
		var value float64
		if err := conn.QueryRow("SELECT unit, value FROM samples WHERE metric = ?", metric).Scan(&unit, &value); err != nil {
			t.Errorf("%s: %v", metric, err)
			continue
		}
		if unit != w.unit || value < w.value-1e-3 || value > w.value+1e-3 {
			t.Errorf("%s stored as %v %s, want %v %s", metric, value, unit, w.value, w.unit)
		}
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

// BUG: nonsense values were stored: -50 steps, a heart rate of 0. Negative
// amounts and non-positive heart rates are bad_value. Zero steps and a
// negative wrist temperature (a delta) are real and stay.
func TestNonsenseValuesAreSkipped(t *testing.T) {
	p := writeFile(t, "export.xml", wrap(`
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="a" unit="count" `+qaWhen+` value="-50"/>
<Record type="HKQuantityTypeIdentifierActiveEnergyBurned" sourceName="a" unit="kcal" `+qaWhen+` value="-1"/>
<Record type="HKQuantityTypeIdentifierHeartRate" sourceName="a" unit="count/min" `+qaWhen+` value="0"/>
<Record type="HKQuantityTypeIdentifierRestingHeartRate" sourceName="a" unit="count/min" `+qaWhen+` value="-60"/>
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="zero" unit="count" `+qaWhen+` value="0"/>
<Record type="HKQuantityTypeIdentifierAppleSleepingWristTemperature" sourceName="neg" unit="degC" `+qaWhen+` value="-0.4"/>`))
	conn := newDB(t)
	sum, err := Import(conn, p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Inserted != 2 || sum.Skipped[reasonBadValue] != 4 {
		t.Errorf("summary = %+v, want 2 inserted and 4 bad_value", sum)
	}
}

// BUG: strconv.ParseFloat accepts hex floats and underscores, so "0x1p6"
// imported as 64. Only plain decimal and exponent numbers are measurements.
func TestOnlyPlainDecimalNumbersAreAccepted(t *testing.T) {
	p := writeFile(t, "export.xml", wrap(`
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="a" unit="count" `+qaWhen+` value="0x1p6"/>
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="b" unit="count" `+qaWhen+` value="1_000"/>
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="c" unit="count" `+qaWhen+` value="1e2"/>
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="d" unit="count" `+qaWhen+` value="12.5"/>`))
	conn := newDB(t)
	sum, err := Import(conn, p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Inserted != 2 || sum.Skipped[reasonBadValue] != 2 {
		t.Errorf("summary = %+v, want 2 inserted and 2 bad_value", sum)
	}
}

// BUG: a third-party source that writes 97 with unit "%" (Apple writes 0.97)
// would have become 9700 percent. Such values are skipped as bad_value.
func TestPercentAbove100IsSkipped(t *testing.T) {
	p := writeFile(t, "export.xml", wrap(`
<Record type="HKQuantityTypeIdentifierOxygenSaturation" sourceName="apple" unit="%" `+qaWhen+` value="0.97"/>
<Record type="HKQuantityTypeIdentifierOxygenSaturation" sourceName="other" unit="%" `+qaWhen+` value="97"/>
<Record type="HKQuantityTypeIdentifierBodyFatPercentage" sourceName="other" unit="%" `+qaWhen+` value="0.15"/>`))
	conn := newDB(t)
	sum, err := Import(conn, p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Inserted != 2 || sum.Skipped[reasonBadValue] != 1 {
		t.Errorf("summary = %+v, want 2 inserted and 1 bad_value", sum)
	}
	if n := count(t, conn, "SELECT COUNT(*) FROM samples WHERE value > 100"); n != 0 {
		t.Errorf("%d rows above 100 percent were stored", n)
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

// ---- timestamps and time zones --------------------------------------------

func TestAppleTime(t *testing.T) {
	cases := []struct {
		in     string
		ts     int64
		offset int
		ok     bool
	}{
		{"2024-12-01 18:00:00 +0100", 1733072400, 3600, true},
		{"2024-12-01 18:00:00 +0000", 1733076000, 0, true},
		{"2024-12-01 18:00:00 -0000", 1733076000, 0, true},
		{"2024-12-01 18:00:00 +0530", 1733056200, 19800, true},  // India: half-hour offset
		{"2024-12-01 18:00:00 +0545", 1733055300, 20700, true},  // Nepal: 45-minute offset
		{"2024-12-01 18:00:00 -0930", 1733110200, -34200, true}, // Marquesas: negative half hour
		{"2024-12-01 18:00:00 +1400", 1733025600, 50400, true},  // Kiribati, the largest offset
		{"1970-01-01 00:00:00 +0000", 0, 0, true},
		{"2024-02-29 23:59:59 +0100", 1709247599, 3600, true}, // leap day
		{"2023-02-29 12:00:00 +0100", 0, 0, false},            // not a leap year
		{"2024-12-01 24:00:00 +0100", 0, 0, false},
		{"2024-12-01 18:00:00", 0, 0, false}, // no offset: refuse to guess
		{"2024-12-01T18:00:00+01:00", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		ts, off, ok := appleTime(c.in)
		if ok != c.ok || ts != c.ts || off != c.offset {
			t.Errorf("appleTime(%q) = %d, %d, %v; want %d, %d, %v", c.in, ts, off, ok, c.ts, c.offset, c.ok)
		}
	}
}

// The result must not depend on the machine's own time zone. We run the same
// import under several local zones (including one whose offset equals one in
// the file, which makes Go take a different internal path) and expect
// identical rows.
func TestImportIgnoresMachineTimeZone(t *testing.T) {
	// Spring-forward night in Europe: 01:59:59 +0100 and 03:00:00 +0200 are
	// one second apart in real time even though the wall clock jumps an hour.
	p := writeFile(t, "x.xml", wrap(`
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="a" unit="count" startDate="2024-03-31 01:59:59 +0100" endDate="2024-03-31 03:00:00 +0200" value="1"/>
<Record type="HKQuantityTypeIdentifierStepCount" sourceName="b" unit="count" startDate="2024-12-01 18:00:00 +0530" endDate="2024-12-01 18:30:00 +0530" value="2"/>
<Record type="HKCategoryTypeIdentifierSleepAnalysis" sourceName="c" startDate="2024-12-01 23:00:00 -0500" endDate="2024-12-02 07:00:00 -0500" value="HKCategoryValueSleepAnalysisAsleepCore"/>`))

	old := time.Local
	t.Cleanup(func() { time.Local = old })

	var want string
	for _, loc := range []*time.Location{
		time.UTC,
		time.FixedZone("IST", 5*3600+1800),
		time.FixedZone("CET", 3600),
		time.FixedZone("EST", -5*3600),
	} {
		time.Local = loc
		conn := newDB(t)
		if _, err := Import(conn, p, Options{}); err != nil {
			t.Fatal(err)
		}
		got := dump(t, conn)
		if want == "" {
			want = got
			// Check the actual numbers once. 01:59:59 +0100 = 00:59:59 UTC.
			if n := count(t, conn, "SELECT COUNT(*) FROM samples WHERE start_ts = 1711846799 AND end_ts = 1711846800 AND tz_offset = 3600"); n != 1 {
				t.Errorf("DST row wrong:\n%s", got)
			}
			if n := count(t, conn, "SELECT COUNT(*) FROM samples WHERE start_ts = 1733056200 AND tz_offset = 19800"); n != 1 {
				t.Errorf("+0530 row wrong:\n%s", got)
			}
			if n := count(t, conn, "SELECT COUNT(*) FROM sleep_stages WHERE end_ts - start_ts = 8*3600 AND tz_offset = -18000"); n != 1 {
				t.Errorf("sleep across midnight wrong:\n%s", got)
			}
		}
		if got != want {
			t.Errorf("rows differ when the machine zone is %v:\n%s\nwant:\n%s", loc, got, want)
		}
	}
}
