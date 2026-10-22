// Package agents owns pure agent payout policy. No HTTP, SQL or clock globals.
package agents

import (
	"errors"
	"time"
)

const MinPayoutCents int64 = 10000
const PayoutCooldown = 7 * 24 * time.Hour

var (
	ErrBelowMinimum = errors.New("提现金额最低 ¥100")
	ErrInsufficient = errors.New("可提现余额不足")
	ErrCooldown     = errors.New("距上次提现申请未满 7 天")
)

// ValidatePayout checks the request amount against the already-computed
// available balance. The last request time is zero when there is none.
func ValidatePayout(amount, available int64, last time.Time, now time.Time) error {
	if amount < MinPayoutCents {
		return ErrBelowMinimum
	}
	if amount > available {
		return ErrInsufficient
	}
	if !last.IsZero() && now.Sub(last) < PayoutCooldown {
		return ErrCooldown
	}
	return nil
}

// CommissionCents computes floor(amount*pct/100) without int64 overflow.
func CommissionCents(amount int64, pct int) int64 {
	if amount <= 0 || pct <= 0 {
		return 0
	}
	return amount/100*int64(pct) + (amount%100)*int64(pct)/100
}
