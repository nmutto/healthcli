package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// --date makes a past day "today" in the four read commands. Only the date
// the JSON is relative to changes shape-wise; the numbers follow the day.
func TestDateFlagMovesToday(t *testing.T) {
	fixClock(t, overviewNow) // 2026-11-08
	path := seedOverview(t, "full")
	for _, cmd := range []string{"overview", "sleep", "workouts", "heatmap"} {
		t.Run(cmd, func(t *testing.T) {
			out, _, err := run(t, "--db", path, cmd, "--date", "2026-11-01", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var got struct{ Today string }
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatal(err)
			}
			if got.Today != "2026-11-01" {
				t.Errorf("today = %q, want 2026-11-01", got.Today)
			}
			// Naming the real today changes nothing.
			plain, _, err := run(t, "--db", path, cmd, "--json")
			if err != nil {
				t.Fatal(err)
			}
			same, _, err := run(t, "--db", path, cmd, "--date", "2026-11-08", "--json")
			if err != nil || same != plain {
				t.Errorf("--date for the real today differs (err %v)", err)
			}
		})
	}
}

// The overview for a past day ignores everything after that day.
func TestOverviewDateIgnoresLaterSamples(t *testing.T) {
	fixClock(t, overviewNow)
	path := seedOverview(t, "full")
	out, _, err := run(t, "--db", path, "overview", "--date", "2026-11-01", "--no-color")
	if err != nil {
		t.Fatal(err)
	}
	checkViewGolden(t, "overview_date.txt", out)
	js, _, err := run(t, "--db", path, "overview", "--date", "2026-11-01", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var o struct {
		Data struct {
			LastDay string `json:"last_day"`
		}
	}
	if err := json.Unmarshal([]byte(js), &o); err != nil {
		t.Fatal(err)
	}
	if o.Data.LastDay > "2026-11-02" { // the one-day slack for time zones
		t.Errorf("last_day %s is after the chosen day", o.Data.LastDay)
	}
}

func TestDateFlagIsValidated(t *testing.T) {
	fixClock(t, overviewNow)
	path := seedOverview(t, "full")
	cases := map[string]string{
		"nonsense":   "bad --date",
		"2026-13-01": "bad --date",
		"1800-01-01": "year must be between",
		"2026-11-09": "in the future",
	}
	for _, cmd := range []string{"overview", "sleep", "workouts", "heatmap"} {
		for date, want := range cases {
			_, _, err := run(t, "--db", path, cmd, "--date", date)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s --date %s: error %v, want %q", cmd, date, err, want)
			}
		}
	}
}
