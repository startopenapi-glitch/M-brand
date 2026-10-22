package app

import (
 "testing"
 "github.com/jackc/pgx/v5/pgxpool"
)
func isolatedMigrationSchema(t *testing.T){
 t.Helper();original:=pool;schema:="audit_migration_"+randHex(5)
 if _,e:=original.Exec(contextBG(),"CREATE SCHEMA "+schema);e!=nil{t.Fatal(e)}
 cfg:=original.Config().Copy();cfg.ConnConfig.RuntimeParams["search_path"]=schema
 p,e:=pgxpool.NewWithConfig(contextBG(),cfg);if e!=nil{t.Fatal(e)};pool=p
 t.Cleanup(func(){pool=original;p.Close();if _,e:=original.Exec(contextBG(),"DROP SCHEMA "+schema+" CASCADE");e!=nil{t.Error(e)}})
}
func TestCommerceMigrationsFreshAndLegacy(t *testing.T){
 for _,legacy:=range []bool{false,true}{name:="fresh";if legacy{name="v6-with-custom-price"};t.Run(name,func(t *testing.T){
  isolatedMigrationSchema(t)
  if legacy{
   for _,name:=range []string{"001_init.sql","002_seed_case.sql","003_wxpay.sql","004_visitor_trial.sql","004_packs.sql","005_alacarte.sql","005_one_code_one_use.sql","006_agents.sql"}{b,e:=migrationFS.ReadFile("migrations/"+name);if e!=nil{t.Fatal(e)};mustExec(t,string(b))}
   mustExec(t,"CREATE TABLE schema_version(v INT NOT NULL); INSERT INTO schema_version VALUES(6)")
   mustExec(t,"UPDATE item_pricing SET price_cents=777 WHERE kind='logo_h'")
  }
  if e:=migrateDB();e!=nil{t.Fatal(e)};if e:=migrateDB();e!=nil{t.Fatalf("idempotent migration: %v",e)}
  var n int;pool.QueryRow(contextBG(),"SELECT COUNT(*) FROM item_pricing").Scan(&n);if n!=17{t.Fatalf("items %d",n)}
  if legacy{var c int;pool.QueryRow(contextBG(),"SELECT price_cents FROM item_pricing WHERE kind='logo_h'").Scan(&c);if c!=1900{t.Fatalf("new pricing migration not applied: %d",c)}}
  mustExec(t,"UPDATE schema_migrations SET checksum='changed' WHERE name='008_commerce_delivery.sql'")
  if e:=migrateDB();e==nil{t.Fatal("modified migration checksum was accepted")}
 })}
}
