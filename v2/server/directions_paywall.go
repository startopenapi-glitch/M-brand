package app

import (
	"github.com/startopenapi-glitch/M-brand/v2/server/internal/domain/catalog"
	"os"
)

const directionUnlockPriceCents int64 = catalog.DirectionUnlockPriceCents

func directionsPaywallEnabled() bool {
	return os.Getenv("ZEROBRAND_DIRECTIONS_PAYWALL") == "1"
}
