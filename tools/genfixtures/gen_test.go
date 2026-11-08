package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func build(t *testing.T, seed int64) (dir string, xmlBytes []byte) {
	t.Helper()
	dir = t.TempDir()
	if err := generate(dir, profiles["small"], seed, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "apple_health_export", "export.xml"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, b
}

func TestSmallIsWellFormedAndCoversTypes(t *testing.T) {
	_, b := build(t, 1)
	if len(b) > 1<<20 {
		t.Fatalf("small export is %d bytes, want < 1MB", len(b))
	}
	if !bytes.Contains(b, []byte("<!DOCTYPE HealthData")) {
		t.Fatal("missing DOCTYPE")
	}

	types := map[string]int{}
	sleepValues := map[string]bool{}
	workouts := map[string]bool{}
	var modernWorkouts, legacyWorkouts, distanceStats int
	sources := map[string]bool{}
	offsets := map[string]bool{}
	missingValue := 0

	dec := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("not well-formed: %v", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		a := map[string]string{}
		for _, at := range se.Attr {
			a[at.Name.Local] = at.Value
		}
		switch se.Name.Local {
		case "Record":
			types[a["type"]]++
			sources[a["sourceName"]] = true
			if _, has := a["value"]; !has {
				missingValue++
			}
			if s := a["startDate"]; len(s) == 25 {
				offsets[s[20:]] = true
			}
			if strings.HasSuffix(a["type"], "SleepAnalysis") {
				sleepValues[strings.TrimPrefix(a["value"], "HKCategoryValueSleepAnalysis")] = true
			}
		case "Workout":
			workouts[a["workoutActivityType"]] = true
			if a["totalDistance"] == "" {
				modernWorkouts++ // totals live in <WorkoutStatistics> children
			} else {
				legacyWorkouts++
			}
		case "WorkoutStatistics":
			if strings.HasPrefix(a["type"], "HKQuantityTypeIdentifierDistance") {
				distanceStats++
			}
		}
	}

	for _, ty := range []string{"HeartRate", "StepCount", "HeartRateVariabilitySDNN", "BodyMass", "RestingHeartRate", "ActiveEnergyBurned"} {
		if types["HKQuantityTypeIdentifier"+ty] == 0 {
			t.Errorf("no %s records", ty)
		}
	}
	for _, v := range []string{"InBed", "AsleepCore", "AsleepDeep", "AsleepREM", "Awake", "AsleepUnspecified"} {
		if !sleepValues[v] {
			t.Errorf("no sleep stage %s", v)
		}
	}
	if modernWorkouts == 0 || legacyWorkouts == 0 || distanceStats != modernWorkouts+legacyWorkouts {
		t.Errorf("workouts: %d legacy-style, %d statistics-style, %d distance statistics; want both styles", legacyWorkouts, modernWorkouts, distanceStats)
	}
	if len(workouts) != 3 {
		t.Errorf("workout kinds = %v, want running/walking/cycling", workouts)
	}
	if len(sources) < 2 {
		t.Errorf("want iPhone and Watch sources, got %v", sources)
	}
	if !offsets["+0100"] || !offsets["-0500"] {
		t.Errorf("want both +0100 and -0500 offsets, got %v", offsets)
	}
	if missingValue == 0 || types["HKQuantityTypeIdentifierSomethingFromTheFuture"] == 0 || !bytes.Contains(b, []byte("99:99:99")) {
		t.Error("malformed records are missing")
	}
}

func TestDeterministic(t *testing.T) {
	_, a := build(t, 7)
	_, b := build(t, 7)
	_, c := build(t, 8)
	if !bytes.Equal(a, b) {
		t.Error("same seed gave different output")
	}
	if bytes.Equal(a, c) {
		t.Error("different seeds gave identical output")
	}
}

func TestZipContainsXML(t *testing.T) {
	dir, want := build(t, 1)
	zr, err := zip.OpenReader(filepath.Join(dir, "export.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != 1 || zr.File[0].Name != zipEntryName {
		t.Fatalf("unexpected zip contents: %v", zr.File)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, want) {
		t.Error("zipped XML differs from export.xml")
	}
}
