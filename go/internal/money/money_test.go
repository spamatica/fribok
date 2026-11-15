package money

import "testing"

func TestParse(t *testing.T) {
	ok := map[string]Ore{
		"200.00": 20000, "-4": -400, "1234,5": 123450, "0.05": 5,
		"-0.5": -50, ".25": 25, "10.000": 1000, "+7.1": 710,
	}
	for in, want := range ok {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "1.234", "1.2.3", "--1", "1e5"} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) should fail", in)
		}
	}
}

func TestFormat(t *testing.T) {
	if got := Ore(-123456789).String(); got != "-1234567.89" {
		t.Errorf("String = %q", got)
	}
	if got := Ore(-123456789).Format(); got != "-1 234 567,89" {
		t.Errorf("Format = %q", got)
	}
	if got := Ore(5).Format(); got != "0,05" {
		t.Errorf("Format = %q", got)
	}
}
