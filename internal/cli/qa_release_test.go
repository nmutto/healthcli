package cli

import (
	"strings"
	"testing"
)

// Logging a different value for a reading that already exists must not claim
// success: the old value stays, so the user has to be told.
func TestLogDifferentValueSameTimeIsAnError(t *testing.T) {
	db := tempDB(t)
	if _, _, err := run(t, "--db", db, "log", "weight", "70", "--at", "2025-04-01 08:00"); err != nil {
		t.Fatal(err)
	}
	_, _, err := run(t, "--db", db, "log", "weight", "71", "--at", "2025-04-01 08:00")
	if err == nil || !strings.Contains(err.Error(), "already") || !strings.Contains(err.Error(), "70") {
		t.Fatalf("want an error naming the stored value 70, got %v", err)
	}
	// the identical value is still a harmless repeat
	if out, _, err := run(t, "--db", db, "log", "weight", "70", "--at", "2025-04-01 08:00"); err != nil || !strings.Contains(out, "Already logged") {
		t.Fatalf("repeat: %q, %v", out, err)
	}
}

func TestLogRejectsImpossibleValues(t *testing.T) {
	for _, args := range [][]string{
		{"weight", "0"}, {"hr", "0"}, {"hrv_sdnn", "0"}, {"resting_heart_rate", "0"},
		{"steps", "--", "-5"}, {"weight", "--", "-5"},
	} {
		db := tempDB(t)
		if _, _, err := run(t, append([]string{"--db", db, "log"}, args...)...); err == nil {
			t.Errorf("log %v should fail", args)
		}
		out, _, _ := run(t, "--db", db, "query", "SELECT count(*) AS n FROM samples", "--json")
		if !strings.Contains(out, `"n": 0`) {
			t.Errorf("log %v left a row: %s", args, out)
		}
	}
	// zero steps is a legitimate reading
	if _, _, err := run(t, "--db", tempDB(t), "log", "steps", "0"); err != nil {
		t.Errorf("steps 0 should be accepted: %v", err)
	}
}

func TestVersionJSON(t *testing.T) {
	out, _, err := run(t, "version", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"version"`, `"commit"`, `"date"`} {
		if !strings.Contains(out, k) {
			t.Errorf("version --json missing %s: %q", k, out)
		}
	}
}
