package repository

import "testing"

func TestParseUserVipValue(t *testing.T) {
	cases := []struct {
		in     string
		plan   string
		expire int64
		ok     bool
	}{
		{"vip1|1788850000", "vip1", 1788850000, true},
		{"free|", "free", 0, true},
		{"free", "free", 0, true},
		{"vip2|0", "vip2", 0, true},
		{"|123", "", 0, false},     // 空套餐
		{"", "", 0, false},         // 空串
		{"vip1|abc", "", 0, false}, // expire 非数字
	}
	for _, c := range cases {
		plan, exp, ok := ParseUserVipValue(c.in)
		if ok != c.ok || plan != c.plan || exp != c.expire {
			t.Errorf("ParseUserVipValue(%q) = (%q,%d,%v) want (%q,%d,%v)",
				c.in, plan, exp, ok, c.plan, c.expire, c.ok)
		}
	}
}
