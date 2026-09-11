package domain

import (
	"testing"
	"time"
)

func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{StatusPending, StatusSent, true},
		{StatusPending, StatusAcked, true},
		{StatusPending, StatusExpired, true},
		{StatusSent, StatusAcked, true},
		{StatusSent, StatusExpired, true},
		{StatusSent, StatusSent, false}, // 不允许回退
		{StatusSent, StatusPending, false},
		{StatusAcked, StatusExpired, false}, // 终态
		{StatusExpired, StatusAcked, false}, // 终态
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.want {
			t.Errorf("CanTransition(%s,%s)=%v want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestPullable(t *testing.T) {
	now := time.Now()
	window := 72 * time.Hour
	live := &AlertRecord{Status: StatusPending, TriggerAt: now.Add(-time.Hour)}
	acked := &AlertRecord{Status: StatusAcked, TriggerAt: now.Add(-time.Hour)}
	expired := &AlertRecord{Status: StatusPending, TriggerAt: now.Add(-73 * time.Hour)}

	if !Pullable(live, now, window) {
		t.Error("PENDING within window should be pullable")
	}
	if Pullable(acked, now, window) {
		t.Error("ACKED should not be pullable")
	}
	if Pullable(expired, now, window) {
		t.Error("beyond 72h window should not be pullable")
	}
}
