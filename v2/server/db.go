package app

// db.go — PG 连接 / 内嵌迁移 / 通用助手

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/startopenapi-glitch/M-brand/v2/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

//go:embed migrations/*.sql
var migrationFS embed.FS

//go:embed webdist
var webFS embed.FS

func dbURL() string {
	if u := os.Getenv("ZEROBRAND_PG_URL"); u != "" {
		return u
	}
	return "postgres://zerobrand:" + os.Getenv("ZEROBRAND_PG_PASS") + "@127.0.0.1:5432/zerobrand"
}

func initDB() error {
	var err error
	pool, err = platform.OpenDB(context.Background(), dbURL())
	if err != nil {
		return err
	}
	return migrateDB()
}

func migrateDB() error {
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(197704,2)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_version(v INT NOT NULL);
 CREATE TABLE IF NOT EXISTS schema_migrations(name TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	var cur int
	if err = tx.QueryRow(ctx, "SELECT COALESCE(MAX(v),0) FROM schema_version").Scan(&cur); err != nil {
		return err
	}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		files = append(files, e.Name())
	}
	sort.Strings(files)
	for _, name := range files {
		var v int
		if _, err = fmt.Sscanf(name, "%d", &v); err != nil {
			return err
		}
		raw, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(raw))
		var previous string
		e := tx.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE name=$1", name).Scan(&previous)
		if e == nil {
			if previous != digest {
				return fmt.Errorf("迁移 %s 内容已被修改", name)
			}
			continue
		}
		if e != pgx.ErrNoRows {
			return e
		}
		if v <= cur {
			continue
		} // old numeric ledger; migration 007 safely repairs skipped tables
		if _, err = tx.Exec(ctx, string(raw)); err != nil {
			return fmt.Errorf("迁移 %s 失败: %w", name, err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)", name, digest); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_version(v) VALUES($1)", v); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)

}

func audit(actor, action, objType, objID, detail string) {
	if detail == "" {
		detail = "{}"
	}
	pool.Exec(context.Background(),
		"INSERT INTO audit_events(actor,action,object_type,object_id,detail) VALUES ($1,$2,$3,$4,$5::jsonb)",
		actor, action, objType, objID, detail)
}

func newID(n int) string { return platform.NewID(n) }

func genDir() string {
	d := platform.LoadConfig().GenDir
	if d == "" {
		d = filepath.Join(filepath.Dir(os.Args[0]), "generated")
	}
	return d
}
