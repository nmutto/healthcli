package cli

import (
	"bytes"
	"testing"
)

func fakeEnv(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

// A bytes.Buffer is never a terminal, like a pipe or a file.
func TestDetectTermPipe(t *testing.T) {
	ti := detectTerm(&bytes.Buffer{}, fakeEnv(map[string]string{"LANG": "en_US.UTF-8"}), false, false)
	if ti.color || ti.unicode || ti.width != 80 {
		t.Errorf("a pipe should be 80 wide, plain, ASCII: %+v", ti)
	}
}

func TestDetectTermWidth(t *testing.T) {
	cases := []struct {
		columns string
		want    int
	}{{"", 80}, {"120", 100}, {"40", 60}, {"72", 72}, {"abc", 80}, {"0", 80}, {"-5", 80}}
	for _, c := range cases {
		ti := detectTerm(&bytes.Buffer{}, fakeEnv(map[string]string{"COLUMNS": c.columns}), false, false)
		if ti.width != c.want {
			t.Errorf("COLUMNS=%q: width %d, want %d", c.columns, ti.width, c.want)
		}
	}
}

func TestDetectTermColor(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		noColor   bool
		wantColor bool
	}{
		{"pipe, nothing set", nil, false, false},
		{"FORCE_COLOR turns it on for a pipe", map[string]string{"FORCE_COLOR": "1"}, false, true},
		{"FORCE_COLOR=0 does not", map[string]string{"FORCE_COLOR": "0"}, false, false},
		{"NO_COLOR beats FORCE_COLOR", map[string]string{"FORCE_COLOR": "1", "NO_COLOR": "1"}, false, false},
		{"--no-color beats FORCE_COLOR", map[string]string{"FORCE_COLOR": "1"}, true, false},
	}
	for _, c := range cases {
		if got := detectTerm(&bytes.Buffer{}, fakeEnv(c.env), c.noColor, false).color; got != c.wantColor {
			t.Errorf("%s: color = %v, want %v", c.name, got, c.wantColor)
		}
	}
}

func TestUTF8Locale(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"LANG": "en_US.UTF-8"}, true},
		{map[string]string{"LANG": "en_US.utf8"}, true},
		{map[string]string{"LANG": "C"}, false},
		{map[string]string{}, false},
		{map[string]string{"LC_ALL": "C", "LANG": "en_US.UTF-8"}, false}, // LC_ALL wins
		{map[string]string{"LC_ALL": "en_GB.UTF-8", "LANG": "C"}, true},
	}
	for _, c := range cases {
		if got := utf8Locale(fakeEnv(c.env)); got != c.want {
			t.Errorf("%v: %v, want %v", c.env, got, c.want)
		}
	}
}
