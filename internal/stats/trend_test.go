package stats

import "testing"

func TestSummarize(t *testing.T) {
	s := Summarize([]float64{5, 2, 9, 4}, []string{"2024-12-01", "2024-12-02", "2024-12-03", "2024-12-04"})
	want := TrendSummary{DaysWithData: 4, Min: 2, MinDay: "2024-12-02", Max: 9, MaxDay: "2024-12-03", Mean: 5}
	if *s != want {
		t.Errorf("got %+v, want %+v", *s, want)
	}
}

// daysOf makes one value per day from first, so tests can say "10 days of 100".
func daysOf(t *testing.T, first string, values ...float64) map[string]DayValue {
	t.Helper()
	byDay := map[string]DayValue{}
	for i, v := range values {
		day, err := AddDays(first, i)
		if err != nil {
			t.Fatal(err)
		}
		byDay[day] = DayValue{Day: day, Value: v}
	}
	return byDay
}

func TestMeanOver(t *testing.T) {
	byDay := daysOf(t, "2024-12-01", 10, 20, 30)
	if mean, n := MeanOver(byDay, "2024-12-01", "2024-12-05"); mean != 20 || n != 3 {
		t.Errorf("got %v over %d days, want 20 over 3", mean, n)
	}
	if mean, n := MeanOver(byDay, "2024-12-10", "2024-12-12"); mean != 0 || n != 0 {
		t.Errorf("empty range: got %v over %d days", mean, n)
	}
}

func TestWeekChangeFor(t *testing.T) {
	// 14 days ending 2024-12-14: the first week 100, the second 110.
	values := []float64{100, 100, 100, 100, 100, 100, 100, 110, 110, 110, 110, 110, 110, 110}
	c, err := WeekChangeFor(daysOf(t, "2024-12-01", values...), "2024-12-14")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Available || c.Last7Days != 7 || c.PrevDays != 7 || *c.Last7Mean != 110 || *c.PrevMean != 100 || *c.Delta != 10 || *c.DeltaPct != 10 {
		t.Errorf("got %+v", c)
	}

	// Only the last week has data: not available, but its mean is there.
	c, err = WeekChangeFor(daysOf(t, "2024-12-08", 5, 5, 5, 5, 5, 5, 5), "2024-12-14")
	if err != nil {
		t.Fatal(err)
	}
	if c.Available || c.PrevMean != nil || c.Delta != nil || c.Last7Mean == nil || c.PrevDays != 0 {
		t.Errorf("got %+v", c)
	}

	if _, err := WeekChangeFor(nil, "not a day"); err == nil {
		t.Error("a bad day should be an error")
	}
}

func TestSleepBaseline(t *testing.T) {
	// 7 nights before 2024-12-10 with data: enough. The night itself is left out.
	byDay := daysOf(t, "2024-12-03", 7, 7, 7, 7, 7, 7, 7, 1)
	mean, n, ok, err := SleepBaseline(byDay, "2024-12-10")
	if err != nil || !ok || n != 7 || mean != 7 {
		t.Errorf("got %v, %d, %v, %v; want 7, 7, true", mean, n, ok, err)
	}
	// 6 nights: not enough, but n still says how many.
	_, n, ok, err = SleepBaseline(daysOf(t, "2024-12-04", 7, 7, 7, 7, 7, 7), "2024-12-10")
	if err != nil || ok || n != 6 {
		t.Errorf("got %d, %v, %v; want 6, false", n, ok, err)
	}
}
