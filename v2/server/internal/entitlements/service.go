// Package entitlements is the single source of truth for what a job may access.
package entitlements

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type Query interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type KindPack struct{ Kind, Pack string }

type Service struct{ Catalog []KindPack }

func LegacyBoardKind(idx int) string {
	switch idx {
	case 1:
		return "worldview"
	case 2:
		return "logo_logic"
	case 3:
		return "color_typo"
	case 4:
		return "app_overview"
	}
	return ""
}

func contains(xs []string, value string) bool {
	for _, x := range xs {
		if x == value {
			return true
		}
	}
	return false
}

func (s Service) For(ctx context.Context, q Query, jid string) (map[string]bool, error) {
	rights := map[string]bool{"direction": true, "system_card": true, "system": true}
	var packs []string
	var quota string
	var refunded, legacyCodeRights bool
	if err := q.QueryRow(ctx, `SELECT unlocked_packs,COALESCE(quota_code,''),refunded,legacy_code_rights FROM jobs WHERE id=$1`, jid).Scan(&packs, &quota, &refunded, &legacyCodeRights); err != nil {
		return nil, err
	}
	legacy, err := q.Query(ctx, `SELECT DISTINCT pack FROM orders WHERE job_id=$1 AND status='paid' AND pack IN ('all','logo','system','boards','apps')`, jid)
	if err != nil {
		return nil, err
	}
	for legacy.Next() {
		var p string
		if err = legacy.Scan(&p); err != nil {
			break
		}
		packs = append(packs, p)
	}
	le := legacy.Err()
	legacy.Close()
	if err != nil {
		return nil, err
	}
	if le != nil {
		return nil, le
	}
	all := contains(packs, "all")
	if quota != "" && !refunded {
		if legacyCodeRights {
			for _, kind := range []string{"board", "worldview", "logo_logic", "color_typo", "app_overview"} {
				rights[kind] = true
			}
		} else {
			itemKind := "logo_icon"
			_ = q.QueryRow(ctx, `SELECT COALESCE(NULLIF(item_kind,''),'logo_icon') FROM invite_codes WHERE code=$1`, quota).Scan(&itemKind)
			if itemKind != "" {
				rights[itemKind] = true
			}
		}
	}
	for _, spec := range s.Catalog {
		if all || contains(packs, spec.Pack) {
			rights[spec.Kind] = true
		}
	}
	if all || contains(packs, "boards") {
		rights["board"] = true
		for i := 1; i <= 4; i++ {
			rights[LegacyBoardKind(i)] = true
		}
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT oi.item_kind FROM order_items oi JOIN orders o ON o.id=oi.order_id WHERE o.job_id=$1 AND o.status='paid'`, jid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		if err = rows.Scan(&kind); err != nil {
			return nil, err
		}
		rights[kind] = true
	}
	return rights, rows.Err()
}
