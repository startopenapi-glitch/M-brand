package orders

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testOrders(t *testing.T) (*pgxpool.Pool, context.Context, func()) {
	t.Helper()
	dsn := os.Getenv("ZEROBRAND_PG_URL")
	if dsn == "" {
		t.Skip("ZEROBRAND_PG_URL not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "orders_test_" + strings.ToLower(strings.ReplaceAll(time.Now().Format("150405.000000"), ".", ""))
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ddl := []string{
		`CREATE TABLE jobs(id text primary key,status text,stage text,unlocked_packs text[] not null default '{}',quota_code text,refunded boolean not null default false,legacy_code_rights boolean not null default false,directions_unlocked boolean not null default false,unlock_method text not null default '',unlocked_at timestamptz,error text not null default '',updated_at timestamptz not null default now(),delivery_version bigint not null default 0)`,
		`CREATE TABLE orders(id text primary key,amount_cents bigint not null,credited boolean not null default false,mock boolean not null default false,status text not null,wx_transaction_id text not null default '',visitor text not null default '',job_id text,pack text,agent_code text,agent_id text,agent_pct int,paid_at timestamptz,updated_at timestamptz not null default now(),code text not null default '')`,
		`CREATE TABLE invite_codes(code text primary key,max_uses int,used int,note text,bound_visitor text,order_id text)`,
		`CREATE TABLE pack_pricing(pack text primary key)`,
		`CREATE TABLE order_items(id serial primary key,order_id text,item_kind text)`,
		`CREATE TABLE delivery_items(job_id text,kind text,unique(job_id,kind))`,
		`CREATE TABLE agents(id text primary key,code text unique,commission_pct int,total_earned_cents bigint not null default 0)`,
		`CREATE TABLE agent_commissions(id serial primary key,agent_id text,order_id text unique,order_amount_cents bigint,commission_cents bigint)`,
	}
	for _, q := range ddl {
		if _, err = db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	cleanup := func() { db.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() }
	return db, ctx, cleanup
}

func TestSettleDirectionUnlock(t *testing.T) {
	db, ctx, cleanup := testOrders(t)
	defer cleanup()
	if _, err := db.Exec(ctx, `INSERT INTO jobs(id,status,stage) VALUES('J1','waiting_unlock','unlock')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO orders(id,amount_cents,status,job_id,pack) VALUES('O1',990,'pending','J1','directions_unlock')`); err != nil {
		t.Fatal(err)
	}
	s := Service{DB: db, Entitlements: func(context.Context, pgx.Tx, string) (map[string]bool, error) { return map[string]bool{}, nil }, Kinds: nil, NewCode: func() string { return "x" }}
	if err := s.Settle(ctx, "O1", "m", "a", 990, "TX1", "m", "a"); err != nil {
		t.Fatal(err)
	}
	var status, stage, method string
	var unlocked bool
	if err := db.QueryRow(ctx, `SELECT status,stage,directions_unlocked,unlock_method FROM jobs WHERE id='J1'`).Scan(&status, &stage, &unlocked, &method); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || stage != "directions" || !unlocked || method != "wechat" {
		t.Fatalf("state=%s/%s/%v/%s", status, stage, unlocked, method)
	}
}

func TestSettleItemOrderCreatesDeliveryAndCommission(t *testing.T) {
	db, ctx, cleanup := testOrders(t)
	defer cleanup()
	for _, q := range []string{
		`INSERT INTO jobs(id,status,stage) VALUES('J2','queued','boards')`,
		`INSERT INTO pack_pricing(pack) VALUES('items')`,
		`INSERT INTO orders(id,amount_cents,status,job_id,pack,agent_code,agent_id,agent_pct) VALUES('O2',990,'pending','J2','items','AG','A1',40)`,
		`INSERT INTO agents(id,code,commission_pct) VALUES('A1','AG',40)`,
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	// Changing the agent rate after order creation must not reprice this order.
	if _, err := db.Exec(ctx, "UPDATE agents SET commission_pct=35 WHERE id='A1'"); err != nil { t.Fatal(err) }
	s := Service{DB: db, Entitlements: func(context.Context, pgx.Tx, string) (map[string]bool, error) {
		return map[string]bool{"logo_h": true}, nil
	}, Kinds: []string{"logo_h"}, NewCode: func() string { return "x" }}
	if err := s.Settle(ctx, "O2", "m", "a", 990, "TX2", "m", "a"); err != nil {
		t.Fatal(err)
	}
	var commission, earned int64
	if err := db.QueryRow(ctx, `SELECT commission_cents FROM agent_commissions WHERE order_id='O2'`).Scan(&commission); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `SELECT total_earned_cents FROM agents WHERE id='A1'`).Scan(&earned); err != nil {
		t.Fatal(err)
	}
	if commission != 396 || earned != 396 {
		t.Fatalf("commission=%d earned=%d", commission, earned)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM delivery_items WHERE job_id='J2' AND kind='logo_h'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("delivery=%d/%v", n, err)
	}
}
