package stats

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestBaselineNeedsSevenDays(t *testing.T) {
	conn := newDB(t)
	ctx := context.Background()
	fillDays(t, conn, 6, 1000)
	b, err := BaselineFor(ctx, conn, "steps", "2024-12-29", 0)
	if !errors.Is(err, ErrNotEnoughData) || b.N != 6 {
		t.Fatalf("6 days: got %+v, %v; want ErrNotEnoughData with N=6", b, err)
	}

	addSample(t, conn, "step_count", "2024-12-22 12:00 +0100", 1000, watch) // the 7th day
	if _, err := BaselineFor(ctx, conn, "steps", "2024-12-29", 0); err != nil {
		t.Fatalf("7 days: %v", err)
	}
}

func TestBaselineExcludesTargetDayAndUsesWindow(t *testing.T) {
	conn := newDB(t)
	ctx := context.Background()
	fillDays(t, conn, 28, 1000, 3000) // alternating 1000/3000 -> mean 2000
	addSample(t, conn, "step_count", "2024-12-29 12:00 +0100", 99999, watch)
	addSample(t, conn, "step_count", "2024-11-30 12:00 +0100", 99999, watch) // 29 days back: outside

	b, err := BaselineFor(ctx, conn, "step_count", "2024-12-29", 28)
	if err != nil {
		t.Fatal(err)
	}
	if b.N != 28 || b.Mean != 2000 || b.From != "2024-12-01" || b.To != "2024-12-28" {
		t.Errorf("got %+v", b)
	}
	// 14 values 1000 off the mean, 14 values 1000 off: sample stddev = 1000*sqrt(28/27).
	if want := 1000 * math.Sqrt(28.0/27.0); math.Abs(b.Stddev-want) > 1e-9 {
		t.Errorf("stddev %v, want %v", b.Stddev, want)
	}
}

func TestCompare(t *testing.T) {
	b := Baseline{Mean: 2000, Stddev: 500}
	cases := []struct {
		value float64
		label string
		z     float64
		pct   float64
	}{
		{2500, Above, 1, 25}, // exactly at the threshold counts
		{2400, Typical, 0.8, 20},
		{1500, Below, -1, -25},
		{2000, Typical, 0, 0},
	}
	for _, c := range cases {
		got := Compare(c.value, b)
		if got.Label != c.label || math.Abs(got.Z-c.z) > 1e-9 || math.Abs(got.DeltaPct-c.pct) > 1e-9 || got.Delta != c.value-2000 {
			t.Errorf("Compare(%v) = %+v, want label %s z %v pct %v", c.value, got, c.label, c.z, c.pct)
		}
	}

	flat := Baseline{Mean: 79.4, Stddev: 0}
	if got := Compare(79.4, flat); got.Label != Typical || got.Z != 0 {
		t.Errorf("flat same: %+v", got)
	}
	if got := Compare(79.5, flat); got.Label != Above {
		t.Errorf("flat higher: %+v", got)
	}
	if got := Compare(5, Baseline{Mean: 0, Stddev: 0}); got.DeltaPct != 0 || got.HasPct || got.Label != Above {
		t.Errorf("zero mean: %+v", got)
	}
}

func TestFlatBaselineHasZeroStddev(t *testing.T) {
	// 79.4 can't be stored exactly in binary; the mean of seven copies must
	// still give a spread of exactly 0 and compare as typical.
	conn := newDB(t)
	day, _ := time.Parse(DayLayout, "2024-12-29")
	for i := 1; i <= 7; i++ {
		addSample(t, conn, "body_mass", day.AddDate(0, 0, -i).Format(DayLayout)+" 07:00 +0100", 79.4, "manual")
	}
	b, err := BaselineFor(context.Background(), conn, "weight", "2024-12-29", 0)
	if err != nil {
		t.Fatal(err)
	}
	if b.Stddev != 0 || Compare(79.4, b).Label != Typical {
		t.Errorf("got %+v, %+v", b, Compare(79.4, b))
	}
}

func TestCompareAlwaysFinite(t *testing.T) {
	cases := []struct {
		v float64
		b Baseline
	}{
		{1, Baseline{Mean: 1e-320, Stddev: 0}},
		{1e300, Baseline{Mean: 1e-300, Stddev: 1e-300}},
		{-1e300, Baseline{Mean: 1e-5, Stddev: 1e-310}},
		{0, Baseline{}},
		{5, Baseline{Mean: 5}},
		{math.MaxFloat64, Baseline{Mean: -math.MaxFloat64, Stddev: 1}},
	}
	for _, c := range cases {
		got := Compare(c.v, c.b)
		for name, x := range map[string]float64{"delta": got.Delta, "pct": got.DeltaPct, "z": got.Z} {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				t.Errorf("Compare(%v, %+v).%s = %v", c.v, c.b, name, x)
			}
		}
		if got.Label != Above && got.Label != Below && got.Label != Typical {
			t.Errorf("label %q", got.Label)
		}
	}
}

func FuzzCompare(f *testing.F) {
	f.Add(1.0, 2.0, 0.5)
	f.Add(1e300, 1e-300, 0.0)
	f.Fuzz(func(t *testing.T, v, mean, sd float64) {
		for _, x := range []float64{v, mean, sd} {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				t.Skip()
			}
		}
		sd = math.Abs(sd)
		got := Compare(v, Baseline{Mean: mean, Stddev: sd})
		for _, x := range []float64{got.Delta, got.DeltaPct, got.Z} {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				t.Fatalf("Compare(%v, mean %v, sd %v) = %+v", v, mean, sd, got)
			}
		}
	})
}

func TestMeanStddevNeverNaN(t *testing.T) {
	for _, vals := range [][]float64{
		{5, 5, 5, 5, 5, 5, 5}, {0, 0, 0, 0, 0, 0, 0}, {1e-300, 2e-300, 1e-300, 2e-300, 1e-300, 2e-300, 1e-300},
		{1e150, -1e150, 1e150, -1e150, 1e150, -1e150, 1e150}, {79.4, 79.4, 79.4, 79.4, 79.4, 79.4, 79.4},
	} {
		m, sd := meanStddev(vals)
		if math.IsNaN(m) || math.IsInf(m, 0) || math.IsNaN(sd) || math.IsInf(sd, 0) || sd < 0 {
			t.Errorf("%v -> %v %v", vals, m, sd)
		}
	}
}
