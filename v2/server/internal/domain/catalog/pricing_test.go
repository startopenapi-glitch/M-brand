package catalog

import "testing"

func TestPrices(t *testing.T) {
	if BundlePriceCents != 9900 || DirectionUnlockPriceCents != 990 {
		t.Fatal("price drift")
	}
}
