package entitlements

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testEnt(t *testing.T) (*pgxpool.Pool, context.Context, func()) {
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
	schema := "ent_test_" + strings.ToLower(strings.ReplaceAll(time.Now().Format("150405.000000"), ".", ""))
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
	for _, q := range []string{
		`CREATE TABLE jobs(id text primary key,unlocked_packs text[] not null default '{}',quota_code text,refunded boolean not null default false,legacy_code_rights boolean not null default false)`,
		`CREATE TABLE orders(id text primary key,job_id text,status text,pack text)`,
		`CREATE TABLE invite_codes(code text primary key,item_kind text)`,
		`CREATE TABLE order_items(id serial primary key,order_id text,item_kind text)`,
	} {
		if _, err = db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return db, ctx, func() { db.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() }
}

func TestForQuotaPacksItemsAndLegacy(t *testing.T) {
	db, ctx, cleanup := testEnt(t)
	defer cleanup()
	svc := Service{Catalog: []KindPack{{Kind: "logo_h", Pack: "logo"}, {Kind: "color_typo", Pack: "system"}}}
	_, _ = db.Exec(ctx, `INSERT INTO jobs(id,quota_code) VALUES('J1','CODE1')`)
	_, _ = db.Exec(ctx, `INSERT INTO invite_codes(code,item_kind) VALUES('CODE1','logo_h')`)
	r, err := svc.For(ctx, db, "J1")
	if err != nil || !r["direction"] || !r["logo_h"] {
		t.Fatalf("quota %v %v", r, err)
	}
	_, _ = db.Exec(ctx, `INSERT INTO jobs(id,quota_code,legacy_code_rights) VALUES('J2','OLD',true)`)
	_, _ = db.Exec(ctx, `INSERT INTO invite_codes(code,item_kind) VALUES('OLD','logo_icon')`)
	r, err = svc.For(ctx, db, "J2")
	if err != nil || !r["board"] || !r["worldview"] {
		t.Fatalf("legacy %v %v", r, err)
	}
	_, _ = db.Exec(ctx, `INSERT INTO jobs(id,unlocked_packs) VALUES('J3',ARRAY['logo'])`)
	r, err = svc.For(ctx, db, "J3")
	if err != nil || !r["logo_h"] || r["color_typo"] {
		t.Fatalf("pack %v %v", r, err)
	}
	_, _ = db.Exec(ctx, `INSERT INTO jobs(id) VALUES('J4')`)
	_, _ = db.Exec(ctx, `INSERT INTO orders(id,job_id,status,pack) VALUES('O4','J4','paid','system')`)
	_, _ = db.Exec(ctx, `INSERT INTO order_items(order_id,item_kind) VALUES('O4','logo_h')`)
	r, err = svc.For(ctx, db, "J4")
	if err != nil || !r["color_typo"] || !r["logo_h"] {
		t.Fatalf("items %v %v", r, err)
	}
}
