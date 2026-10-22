package support

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testService(t *testing.T) (*Service, context.Context, func()) {
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
	schema := "support_test_" + strings.ToLower(strings.ReplaceAll(time.Now().Format("150405.000000"), ".", ""))
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
		`CREATE TABLE jobs(id text primary key, visitor text not null)`,
		`CREATE TABLE orders(id text primary key, visitor text not null)`,
		`CREATE TABLE support_cases(id text primary key,request_key text unique not null,visitor text not null,job_id text,order_id text,category text not null,description text not null,status text not null default 'open',resolution text not null default '',version bigint not null default 0,created_at timestamptz not null default now(),updated_at timestamptz not null default now())`,
		`CREATE TABLE support_events(id bigserial primary key,case_id text not null,actor text not null,action text not null,note text not null default '',created_at timestamptz not null default now())`,
	}
	for _, q := range ddl {
		if _, err = db.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	cleanup := func() { db.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() }
	return &Service{DB: db}, ctx, cleanup
}

func TestCreateIdempotentOwnerAndReview(t *testing.T) {
	svc, ctx, cleanup := testService(t)
	defer cleanup()
	if _, err := svc.DB.Exec(ctx, `INSERT INTO jobs(id,visitor) VALUES('J1','V1')`); err != nil {
		t.Fatal(err)
	}
	in := CreateInput{RequestKey: "REQ-12345678", Visitor: "V1", JobID: "J1", Category: "generation", Description: "方向未生成"}
	row, inserted, err := svc.Create(ctx, in)
	if err != nil || !inserted {
		t.Fatalf("create=%v inserted=%v", err, inserted)
	}
	again, inserted, err := svc.Create(ctx, in)
	if err != nil || inserted || again.ID != row.ID {
		t.Fatalf("idempotency %v/%v", err, inserted)
	}
	bad := in
	bad.Visitor = "V2"
	if _, _, err := svc.Create(ctx, bad); err != ErrNotFound {
		t.Fatalf("owner err=%v", err)
	}
	rows, err := svc.AdminList(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list=%v/%d", err, len(rows))
	}
	reviewed, err := svc.Review(ctx, ReviewInput{ID: row.ID, Status: "resolved", Resolution: "已补发", Actor: "admin"})
	if err != nil || reviewed.Status != "resolved" {
		t.Fatalf("review=%v/%s", err, reviewed.Status)
	}
	var n int
	if err = svc.DB.QueryRow(ctx, `SELECT count(*) FROM support_events WHERE case_id=$1`, row.ID).Scan(&n); err != nil || n < 2 {
		t.Fatalf("events=%d/%v", n, err)
	}
}

func TestReviewVersionConflictAndAuditAtomic(t *testing.T){
 svc,ctx,cleanup:=testService(t);defer cleanup()
 svc.DB.Exec(ctx,`INSERT INTO support_cases(id,request_key,visitor,category,description) VALUES('R','REQUEST-R','V','other','test')`)
 version:=int64(0)
 in:=ReviewInput{ID:"R",Status:"resolved",Resolution:"ok",Actor:"admin",Version:&version}
 if _,e:=svc.Review(ctx,in);e!=nil{t.Fatal(e)}
 if _,e:=svc.Review(ctx,in);e!=ErrConflict{t.Fatalf("stale must conflict: %v",e)}
 var n int;svc.DB.QueryRow(ctx,`SELECT count(*) FROM support_events WHERE case_id='R'`).Scan(&n);if n!=1{t.Fatal(n)}
 svc.DB.Exec(ctx,`DROP TABLE support_events`)
 version=1;in.Status="reviewing"
 if _,e:=svc.Review(ctx,in);e==nil{t.Fatal("missing audit must fail")}
 var status string;svc.DB.QueryRow(ctx,`SELECT status FROM support_cases WHERE id='R'`).Scan(&status);if status!="resolved"{t.Fatal("failed audit committed change")}
}

func TestAdminListOrderFilterBeforeLimit(t *testing.T){
 svc,ctx,cleanup:=testService(t);defer cleanup()
 for i:=0;i<205;i++{_,e:=svc.DB.Exec(ctx,`INSERT INTO support_cases(id,request_key,visitor,order_id,category,description,status,created_at) VALUES($1,$1,'V',$1,'other','test','resolved',now()-($2::int*interval '1 day'))`,"ORDER-"+time.Unix(int64(i),0).UTC().Format("150405"),i);if e!=nil{t.Fatal(e)}}
 rows,e:=svc.AdminList(ctx,"ORDER-000324");if e!=nil||len(rows)!=1||rows[0].OrderID!="ORDER-000324"{t.Fatalf("old linked support: %v %v",rows,e)}
}
