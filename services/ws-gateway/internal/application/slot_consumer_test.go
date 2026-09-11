package application

import "testing"

func TestRuleFromEventID(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"r5-v1-w100", 5},
		{"r123-v999-w1", 123},
		{"", 0},
		{"bad", 0},
		{"r-x", 0},
	}
	for _, c := range cases {
		if got := ruleFromEventID(c.in); got != c.want {
			t.Errorf("ruleFromEventID(%q)=%d want %d", c.in, got, c.want)
		}
	}
}
