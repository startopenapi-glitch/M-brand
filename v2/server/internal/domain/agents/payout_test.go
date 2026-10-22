package agents

import (
	"testing"
	"time"
)

func TestValidatePayout(t *testing.T) {
	now := time.Now()
	if ValidatePayout(9999, 100000, time.Time{}, now) != ErrBelowMinimum {
		t.Fatal("minimum")
	}
	if ValidatePayout(50000, 40000, time.Time{}, now) != ErrInsufficient {
		t.Fatal("available")
	}
	if ValidatePayout(50000, 100000, now.Add(-6*24*time.Hour), now) != ErrCooldown {
		t.Fatal("cooldown")
	}
	if err := ValidatePayout(50000, 100000, now.Add(-7*24*time.Hour), now); err != nil {
		t.Fatal(err)
	}
}

func TestCommissionCents(t *testing.T) {
	cases := []struct {
		amount int64
		pct    int
		want   int64
	}{{990, 40, 396}, {199, 40, 79}, {0, 40, 0}, {100, 0, 0}}
	for _, c := range cases {
		if got := CommissionCents(c.amount, c.pct); got != c.want {
			t.Fatalf("%d/%d=%d want %d", c.amount, c.pct, got, c.want)
		}
	}
}
