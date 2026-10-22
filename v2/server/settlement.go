package app

import (
	"context"

	"github.com/startopenapi-glitch/M-brand/v2/server/internal/orders"
	"github.com/jackc/pgx/v5"
)

// settleOrder is the legacy package-main adapter. The transaction lives in
// internal/orders; this file only supplies channel identity and domain callbacks.
func settleOrder(id, mchid, appid string, totalCents int64, txid string) error {
	wxcfg.mu.RLock()
	wantM, wantA := wxcfg.mchid, wxcfg.appid
	wxcfg.mu.RUnlock()
	s := orders.Service{
		DB: pool,
		Entitlements: func(ctx context.Context, tx pgx.Tx, jid string) (map[string]bool, error) {
			return jobEntitlements(ctx, tx, jid)
		},
		Kinds:   itemKinds(),
		NewCode: func() string { return randHex(8) },
	}
	return s.Settle(context.Background(), id, mchid, appid, totalCents, txid, wantM, wantA)
}

func itemKinds() []string {
	specs := itemSpecs(BrandForm{})
	out := make([]string, 0, len(specs))
	for _, sp := range specs {
		out = append(out, sp.Kind)
	}
	return out
}
