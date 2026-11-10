package metrics

import "testing"

func TestParseNumber(t *testing.T) {
	good := map[string]float64{"12": 12, "-1.5": -1.5, "1e3": 1000, ".5": 0.5, "+4": 4}
	for in, want := range good {
		if got, ok := ParseNumber(in); !ok || got != want {
			t.Errorf("ParseNumber(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "0x1p6", "1_0", "NaN", "Inf", "-inf", "1e999", "abc", "1 0"} {
		if _, ok := ParseNumber(in); ok {
			t.Errorf("ParseNumber(%q) should fail", in)
		}
	}
}

func TestCheckValue(t *testing.T) {
	spo2, _ := Lookup("spo2")
	steps, _ := Lookup("steps")
	fat, _ := Lookup("body_fat_percentage")
	cases := []struct {
		m    Metric
		v    float64
		fail bool
	}{
		{spo2, 97, false}, {spo2, 50, false}, {spo2, 49.9, true}, {spo2, 0.97, true}, {spo2, 100.5, true},
		{fat, 0, false}, {fat, 100, false}, {fat, 100.1, true},
		{steps, 1e7, false}, {steps, 1e7 + 1, true},
	}
	for _, c := range cases {
		if err := c.m.CheckValue(c.v); (err != nil) != c.fail {
			t.Errorf("%s CheckValue(%v) = %v, fail=%v", c.m.Name, c.v, err, c.fail)
		}
	}
}
