package stats

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestQuintileThresholds(t *testing.T) {
	if got := QuintileThresholds(nil); got != nil {
		t.Errorf("no values: got %v, want nil", got)
	}
	// 1..10: nearest rank gives the 2nd, 4th, 6th and 8th value.
	var v []float64
	for i := 10; i >= 1; i-- { // unsorted on purpose
		v = append(v, float64(i))
	}
	if got, want := QuintileThresholds(v), []float64{2, 4, 6, 8}; !reflect.DeepEqual(got, want) {
		t.Errorf("thresholds = %v, want %v", got, want)
	}
	// 1..10 split evenly: two days per level.
	counts := map[int]int{}
	for _, x := range v {
		counts[HeatLevel(x, QuintileThresholds(v))]++
	}
	for l := 1; l <= 5; l++ {
		if counts[l] != 2 {
			t.Errorf("level %d has %d days, want 2 (%v)", l, counts[l], counts)
		}
	}
}

func TestHeatLevelTiesShareALevel(t *testing.T) {
	// Eight equal values and two others: thresholds are all 5, so 1 and 5
	// share level 1 (a tie is never split) and only 9 reaches level 5.
	vals := []float64{1, 5, 5, 5, 5, 5, 5, 5, 5, 9}
	th := QuintileThresholds(vals)
	if want := []float64{5, 5, 5, 5}; !reflect.DeepEqual(th, want) {
		t.Fatalf("thresholds = %v, want %v", th, want)
	}
	if HeatLevel(1, th) != 1 || HeatLevel(5, th) != 1 || HeatLevel(9, th) != 5 {
		t.Errorf("levels: 1->%d 5->%d 9->%d", HeatLevel(1, th), HeatLevel(5, th), HeatLevel(9, th))
	}
}

func TestLayoutHeatmapGapsFutureAndFlat(t *testing.T) {
	// 2026-11-04 is a Wednesday; 2 weeks start on Monday 2026-10-26.
	byDay := map[string]float64{"2026-10-26": 100, "2026-10-28": 300, "2026-11-04": 200}
	h := layoutHeatmap("step_count", "count", "2026-11-04", "2026-10-26", 2, byDay, false)
	if len(h.Weeks) != 2 || len(h.Weeks[0]) != 7 {
		t.Fatalf("grid is %d weeks", len(h.Weeks))
	}
	if h.Weeks[1][2].Day != "2026-11-04" || h.Weeks[1][2].Value == nil {
		t.Errorf("Wednesday of week 2 = %+v", h.Weeks[1][2])
	}
	if c := h.Weeks[0][1]; c.Value != nil || c.Level != 0 || c.Future {
		t.Errorf("a day without data is a gap with level 0: %+v", c)
	}
	if c := h.Weeks[1][3]; !c.Future || c.Value != nil || c.Level != 0 {
		t.Errorf("Thursday after today is future: %+v", c)
	}
	if h.DaysWithData != 3 || h.Empty() {
		t.Errorf("days with data = %d", h.DaysWithData)
	}
	if h.Weeks[0][0].Level >= h.Weeks[0][2].Level {
		t.Errorf("100 should rank below 300: %+v %+v", h.Weeks[0][0], h.Weeks[0][2])
	}

	flat := layoutHeatmap("step_count", "count", "2026-11-04", "2026-10-26", 2,
		map[string]float64{"2026-10-26": 7, "2026-10-27": 7, "2026-11-01": 7}, false)
	for _, w := range flat.Weeks {
		for _, c := range w {
			if c.Value != nil && c.Level != flatLevel {
				t.Errorf("all-equal data should be level %d, got %+v", flatLevel, c)
			}
		}
	}

	empty := layoutHeatmap("step_count", "count", "2026-11-04", "2026-10-26", 2, nil, false)
	if !empty.Empty() || len(empty.Thresholds) != 0 {
		t.Errorf("empty heatmap: %+v", empty)
	}
}

func TestBuildHeatmapFromDatabase(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "step_count", "2026-11-02 12:00 +0000", 4000, watch) // Monday of the last week
	addSample(t, conn, "step_count", "2026-11-02 13:00 +0000", 99999, iphone)
	addSample(t, conn, "step_count", "2026-11-04 23:30 +0100", 8000, watch) // local day, not UTC day
	addSample(t, conn, "step_count", "2026-10-01 12:00 +0000", 1, watch)    // before the window

	now := time.Date(2026, 11, 4, 15, 0, 0, 0, time.UTC)
	h, err := BuildHeatmap(context.Background(), conn, "steps", 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if h.Metric != "step_count" || h.From != "2026-10-26" || h.DaysWithData != 2 {
		t.Fatalf("heatmap = %+v", h)
	}
	if v := h.Weeks[1][0].Value; v == nil || *v != 4000 {
		t.Errorf("Monday = %v, want 4000 (Watch only)", v)
	}
	if v := h.Weeks[1][2].Value; v == nil || *v != 8000 {
		t.Errorf("Wednesday = %v, want 8000", v)
	}
	if _, err := BuildHeatmap(context.Background(), conn, "steps", 0, now); err == nil {
		t.Error("0 weeks should be rejected")
	}
	if _, err := BuildHeatmap(context.Background(), conn, "nope", 2, now); err == nil {
		t.Error("unknown metric should be rejected")
	}
	// On a Sunday the last week is complete (no future cells).
	sun := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
	h, _ = BuildHeatmap(context.Background(), conn, "steps", 1, sun)
	if h.From != "2026-11-02" || h.Weeks[0][6].Future {
		t.Errorf("sunday layout: from %s, last cell %+v", h.From, h.Weeks[0][6])
	}
}
