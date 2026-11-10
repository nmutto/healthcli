package cli

import (
	"math"
	"strings"
	"testing"

	"github.com/nmutto/healthcli/internal/stats"
)

// The trend line uses the palette of the heatmaps: the hue of the metric,
// dim for low bars and bright for high ones. Never one flat cyan.
func TestTrendLineUsesTheMetricPalette(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6, 7, 8, math.NaN()}
	st := style{color: true}
	cases := []struct {
		metric string
		hue    string
		first  string // colour of the lowest bar
		last   string // colour of the tallest bar
	}{
		{"step_count", stats.HueOrange, "38;5;94", "38;5;214"},
		{"resting_heart_rate", stats.HueRed, "38;5;52", "38;5;196"},
		{"respiratory_rate", stats.HueCyan, "38;5;23", "38;5;51"},
		{"body_mass", stats.HueViolet, "38;5;53", "38;5;99"},
		{"sleep", stats.HueBlue, "38;5;24", "38;5;117"},
	}
	for _, c := range cases {
		if got := stats.HueFor(c.metric); got != c.hue && !(c.hue == stats.HueOrange && got == "") {
			t.Errorf("%s: hue %q, want %q", c.metric, got, c.hue)
		}
		line := trendLine(values, stats.HueFor(c.metric), unicodeGlyphs, st)
		if !strings.HasPrefix(line, "\x1b["+c.first+"m") {
			t.Errorf("%s: lowest bar should start with colour %s: %q", c.metric, c.first, line)
		}
		if !strings.Contains(line, "\x1b["+c.last+"m") {
			t.Errorf("%s: tallest bar should use colour %s: %q", c.metric, c.last, line)
		}
		if c.hue != stats.HueCyan && strings.Contains(line, "\x1b[36m") {
			t.Errorf("%s: the old flat cyan is back: %q", c.metric, line)
		}
		if !strings.Contains(line, "\x1b[2m·") && !strings.Contains(line, "\x1b[2m"+string(unicodeGlyphs.gap)) {
			t.Errorf("%s: the gap should be dim: %q", c.metric, line)
		}
		// Colour adds nothing but colour.
		if plain := trendLine(values, stats.HueFor(c.metric), unicodeGlyphs, style{}); stripANSI(line) != plain {
			t.Errorf("%s: stripped %q != plain %q", c.metric, stripANSI(line), plain)
		}
	}
}
