// Package orders owns the durable order settlement transaction.
// It must not import Gin or a payment-channel adapter.
package orders

import (
	"context"
	"fmt"
	"strings"

	"github.com/startopenapi-glitch/M-brand/v2/server/internal/domain/agents"
	"github.com/startopenapi-glitch/M-brand/v2/server/internal/domain/payments"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Entitlements func(context.Context, pgx.Tx, string) (map[string]bool, error)

type Service struct {
	DB           *pgxpool.Pool
	Entitlements Entitlements
	Kinds        []string
	NewCode      func() string
}

// Settle commits a verified channel fact, order rights, durable queue and
// commission in one transaction. Expected channel identities are supplied by
// the adapter; no global payment config is read here.
func (s Service) Settle(ctx context.Context, id, mchid, appid string, totalCents int64, txid, wantMchid, wantAppid string) error {
	if !payments.ValidIdentity(mchid, appid, wantMchid, wantAppid, txid) {
		return fmt.Errorf("交易身份不匹配")
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var scope string
	if err = tx.QueryRow(ctx, "SELECT COALESCE(job_id,'') FROM orders WHERE id=$1", id).Scan(&scope); err != nil {
		return err
	}
	if scope != "" {
		var locked string
		if err = tx.QueryRow(ctx, "SELECT id FROM jobs WHERE id=$1 FOR UPDATE", scope).Scan(&locked); err != nil {
			return err
		}
	}
	var cents int64
	var credited, mock bool
	var status, storedTx, vid, job, pack, agentCode, agentID string
	var pct *int
	err = tx.QueryRow(ctx, `SELECT amount_cents,credited,mock,status,COALESCE(wx_transaction_id,''),COALESCE(visitor,''),COALESCE(job_id,''),COALESCE(pack,''),COALESCE(agent_code,''),COALESCE(agent_id,''),agent_pct FROM orders WHERE id=$1 FOR UPDATE`, id).Scan(&cents, &credited, &mock, &status, &storedTx, &vid, &job, &pack, &agentCode, &agentID, &pct)
	if err != nil {
		return fmt.Errorf("订单不存在")
	}
	if cents != totalCents {
		return fmt.Errorf("金额不符")
	}
	if storedTx != "" && storedTx != txid {
		return fmt.Errorf("交易编号不匹配")
	}
	if _, err = tx.Exec(ctx, `UPDATE orders SET status='paid',paid_at=COALESCE(paid_at,now()),wx_transaction_id=$2,updated_at=now() WHERE id=$1`, id, txid); err != nil {
		return err
	}
	if pack == "directions_unlock" && !credited {
		if job == "" {
			return fmt.Errorf("方向解锁订单缺少任务")
		}
		if _, err = tx.Exec(ctx, `UPDATE jobs SET directions_unlocked=true,stage='directions',status='queued',unlock_method='wechat',unlocked_at=now(),error='',updated_at=now() WHERE id=$1`, job); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE orders SET credited=true WHERE id=$1", id); err != nil {
			return err
		}
		credited = true
	}
	if !credited {
		if job == "" {
			target := "AUTO" + strings.ToUpper(s.NewCode())
			if _, err = tx.Exec(ctx, `INSERT INTO invite_codes(code,max_uses,used,note,bound_visitor,order_id) VALUES($1,1,0,$2,$3,$4)`, target, "自动发码 · 订单 "+id, vid, id); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "UPDATE orders SET code=$2 WHERE id=$1", id, target); err != nil {
				return err
			}
		} else {
			var locked string
			if err = tx.QueryRow(ctx, "SELECT id FROM jobs WHERE id=$1 FOR UPDATE", job).Scan(&locked); err != nil {
				return err
			}
			var n int
			if err = tx.QueryRow(ctx, "SELECT COUNT(*) FROM order_items WHERE order_id=$1", id).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				var exists bool
				if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pack_pricing WHERE pack=$1)", pack).Scan(&exists); err != nil || !exists {
					return fmt.Errorf("订单交付快照无效")
				}
				if _, err = tx.Exec(ctx, `UPDATE jobs SET unlocked_packs=CASE WHEN $2='all' THEN ARRAY['all'] WHEN $2=ANY(unlocked_packs) THEN unlocked_packs ELSE array_append(unlocked_packs,$2) END WHERE id=$1`, job, pack); err != nil {
					return err
				}
			}
			rights, e := s.Entitlements(ctx, tx, job)
			if e != nil {
				return e
			}
			for _, kind := range s.Kinds {
				if !rights[kind] {
					continue
				}
				if _, err = tx.Exec(ctx, `INSERT INTO delivery_items(job_id,kind) VALUES($1,$2) ON CONFLICT DO NOTHING`, job, kind); err != nil {
					return err
				}
			}
			if _, err = tx.Exec(ctx, `UPDATE jobs SET delivery_version=delivery_version+1,status=CASE WHEN status IN ('running','boards_running') THEN status ELSE 'queued' END,stage='boards',error='',updated_at=now() WHERE id=$1`, job); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, "UPDATE orders SET credited=true WHERE id=$1", id); err != nil {
			return err
		}
	}
	if !mock && agentCode != "" && agentID == "" {
		var p int
		e := tx.QueryRow(ctx, "SELECT id,commission_pct FROM agents WHERE code=$1", agentCode).Scan(&agentID, &p)
		if e != nil && e != pgx.ErrNoRows {
			return e
		}
		if e == nil {
			pct = &p
		}
	}
	if !mock && agentID != "" && pct != nil {
		if *pct <= 0 || *pct > 90 {
			return fmt.Errorf("佣金快照无效")
		}
		commission := agents.CommissionCents(cents, *pct)
		if commission > 0 {
			tag, e := tx.Exec(ctx, `INSERT INTO agent_commissions(agent_id,order_id,order_amount_cents,commission_cents) VALUES($1,$2,$3,$4) ON CONFLICT(order_id) DO NOTHING`, agentID, id, cents, commission)
			if e != nil {
				return e
			}
			if tag.RowsAffected() == 1 {
				if _, err = tx.Exec(ctx, "UPDATE agents SET total_earned_cents=total_earned_cents+$2 WHERE id=$1", agentID, commission); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit(ctx)
}
