package cli

import (
	"math"
	"reflect"
	"testing"
)

func TestLargestRemainder(t *testing.T) {
	cases := []struct {
		name  string
		parts []int64
		total int
		want  []int
	}{
		{"three equal parts of 100 still add to 100", []int64{1, 1, 1}, 100, []int{34, 33, 33}},
		{"exact split", []int64{1, 3}, 8, []int{2, 6}},
		{"biggest leftovers win the spare units", []int64{5, 4, 1}, 7, []int{3, 3, 1}}, // 3.5, 2.8, 0.7 -> floors 3,2,0; spares go to .8 and .7
		{"zero part never gets a unit", []int64{0, 2, 1}, 4, []int{0, 3, 1}},
		{"all zero", []int64{0, 0}, 10, []int{0, 0}},
		{"no parts", nil, 10, []int{}},
		{"zero total", []int64{3, 4}, 0, []int{0, 0}},
	}
	for _, c := range cases {
		got := largestRemainder(c.parts, c.total)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: largestRemainder(%v, %d) = %v, want %v", c.name, c.parts, c.total, got, c.want)
		}
	}
}

// Whatever the sizes, the cells always add up to the width.
func TestLargestRemainderAlwaysSums(t *testing.T) {
	for total := 1; total <= 40; total++ {
		for _, parts := range [][]int64{{1, 1, 1}, {7, 13, 29, 2}, {1, 1000000}, {5}} {
			sum := 0
			for _, n := range largestRemainder(parts, total) {
				sum += n
			}
			if sum != total {
				t.Errorf("largestRemainder(%v, %d) sums to %d", parts, total, sum)
			}
		}
	}
}

func TestBandBar(t *testing.T) {
	g := unicodeGlyphs
	// Width 9: the track covers mean±3sd over cells 0..8, so one sd is 1.33
	// cells. The shaded "typical" part is mean±1sd = cells 3..5.
	cases := []struct {
		name                string
		value, mean, stddev float64
		want                string
	}{
		{"on the mean", 100, 100, 10, "···░●░···"},
		{"a little above", 110, 100, 10, "···░░●···"},
		{"far above pins right", 500, 100, 10, "···░░░··●"},
		{"far below pins left", -500, 100, 10, "●··░░░···"},
		{"no spread, same value", 50, 50, 0, "···░●░···"},
	}
	for _, c := range cases {
		if got := g.bandBar(c.value, c.mean, c.stddev, 9); got != c.want {
			t.Errorf("%s: bandBar = %q, want %q", c.name, got, c.want)
		}
	}
	if got := asciiGlyphs.bandBar(100, 100, 10, 9); got != "...=*=..." {
		t.Errorf("ascii band = %q", got)
	}
	if got := g.bandBar(1, 1, 1, 2); got != "··" {
		t.Errorf("too narrow for a band should be a plain track, got %q", got)
	}
}

func TestStackedBar(t *testing.T) {
	segs := []segment{{"core", 240}, {"rem", 60}, {"deep", 90}, {"awake", 10}}
	got := unicodeGlyphs.stackedBar(segs, 40)
	if n := len([]rune(got)); n != 40 {
		t.Fatalf("width %d, want 40: %q", n, got)
	}
	// 400 minutes over 40 cells: 24 core, 6 rem, 9 deep, and 1 awake.
	want := "▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▓▓▓▓▓▓█████████░"
	if got != want {
		t.Errorf("stackedBar = %q, want %q", got, want)
	}
	if got := asciiGlyphs.stackedBar(segs, 10); len(got) != 10 {
		t.Errorf("ascii width %d", len(got))
	}
	if got := unicodeGlyphs.stackedBar(nil, 10); got != "" {
		t.Errorf("no segments should draw nothing, got %q", got)
	}
}

func TestDeltaArrow(t *testing.T) {
	cases := []struct {
		pct         float64
		up, down, f string
	}{{17.2, "▲", "", ""}, {-6, "", "▼", ""}, {0.4, "", "", "–"}, {-0.4, "", "", "–"}, {0, "", "", "–"}}
	for _, c := range cases {
		got := unicodeGlyphs.deltaArrow(c.pct)
		want := c.up + c.down + c.f
		if got != want {
			t.Errorf("deltaArrow(%v) = %q, want %q", c.pct, got, want)
		}
	}
	if asciiGlyphs.deltaArrow(5) != "^" || asciiGlyphs.deltaArrow(-5) != "v" || asciiGlyphs.deltaArrow(0) != "-" {
		t.Error("ascii arrows wrong")
	}
}

func TestGlyphSparklineASCII(t *testing.T) {
	got := asciiGlyphs.sparkline([]float64{1, math.NaN(), 8})
	if got != "._@" {
		t.Errorf("ascii sparkline = %q", got)
	}
}

func TestFitAndJoinFit(t *testing.T) {
	g := unicodeGlyphs
	if got := fit("hello world", 8, g); got != "hello w…" {
		t.Errorf("fit = %q", got)
	}
	if got := fit("short", 8, g); got != "short" {
		t.Errorf("fit = %q", got)
	}
	if got := joinFit([]string{"aaaa", "bbbb", "cccc"}, 12, g); got != "aaaa · bbbb" {
		t.Errorf("joinFit drops the last part first, got %q", got)
	}
	if got := joinFit([]string{"aaaaaaaaaa", "b"}, 5, g); got != "aaaa…" {
		t.Errorf("joinFit last resort = %q", got)
	}
}

func TestPctText(t *testing.T) {
	cases := map[float64]string{17.4: "+17%", -6.6: "-7%", 0.3: "0%", -0.4: "0%", 5000: ">+999%", -5000: "<-999%"}
	for in, want := range cases {
		if got := pctText(in); got != want {
			t.Errorf("pctText(%v) = %q, want %q", in, got, want)
		}
	}
}
