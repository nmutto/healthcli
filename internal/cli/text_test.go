package cli

import (
	"testing"
)

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
