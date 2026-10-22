package app

import (
	"github.com/startopenapi-glitch/M-brand/v2/server/internal/entitlements"
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"os"
	"path/filepath"
	"strings"
)

type commerceQuery interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func requireJobOwner(c *gin.Context, jid string) bool {
	var owner, code string
	if e := pool.QueryRow(c, `SELECT COALESCE(visitor,''),COALESCE(NULLIF(quota_code,''),code,'') FROM jobs WHERE id=$1`, jid).Scan(&owner, &code); e != nil {
		readFailure(c, e, "任务不存在")
		return false
	}
	if !jobOwned(c, visitorID(c), "", owner, code) {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return false
	}
	return true
}

// One source for original-file access, checkout exclusion and worker dispatch.
func jobEntitlements(ctx context.Context, q commerceQuery, jid string) (map[string]bool, error) {
	catalog := make([]entitlements.KindPack, 0)
	for _, sp := range itemSpecs(BrandForm{}) {
		catalog = append(catalog, entitlements.KindPack{Kind: sp.Kind, Pack: sp.Pack})
	}
	return (entitlements.Service{Catalog: catalog}).For(ctx, q, jid)
}

// Canonical generated path; reject symlinks for both PNG and negotiated WebP.
func openArtifact(jid, stored string) (*os.File, error) {
	prefix := "gen/" + jid + "/"
	if !strings.HasPrefix(stored, prefix) {
		return nil, fmt.Errorf("invalid artifact")
	}
	name := strings.TrimPrefix(stored, prefix)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return nil, fmt.Errorf("invalid artifact")
	}
	dir := filepath.Join(genDir(), jid)
	di, err := os.Lstat(dir)
	if err != nil || !di.IsDir() || di.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("invalid artifact directory")
	}
	path := filepath.Join(dir, name)
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("artifact unavailable")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	di2, e := os.Lstat(dir)
	if err != nil || e != nil || !os.SameFile(st, opened) || !os.SameFile(di, di2) {
		f.Close()
		return nil, fmt.Errorf("artifact changed")
	}
	return f, nil
}

func legacyBoardKind(idx int) string { return entitlements.LegacyBoardKind(idx) }
func priorBoardFile(ctx context.Context, q commerceQuery, jid, kind string) string {
	for i := 1; i <= 4; i++ {
		if legacyBoardKind(i) == kind {
			var f string
			if q.QueryRow(ctx, "SELECT file FROM job_images WHERE job_id=$1 AND kind='board' AND idx=$2", jid, i).Scan(&f) == nil {
				return f
			}
		}
	}
	return ""
}

// Persist fulfillment before processing add-ons, including old board filenames.
func markLegacyFulfilled(jid string) error {
	var quota string
	var refunded, done bool
	if e := pool.QueryRow(contextBG(), "SELECT COALESCE(quota_code,''),refunded,legacy_fulfilled FROM jobs WHERE id=$1", jid).Scan(&quota, &refunded, &done); e != nil {
		return e
	}
	if quota == "" || refunded || done {
		return nil
	}
	for i := 1; i <= 4; i++ {
		var f string
		if e := pool.QueryRow(contextBG(), "SELECT file FROM job_images WHERE job_id=$1 AND ((kind='board' AND idx=$2) OR kind=$3) AND COALESCE(file,'')<>'' ORDER BY id DESC LIMIT 1", jid, i, legacyBoardKind(i)).Scan(&f); e != nil {
			if e == pgx.ErrNoRows {
				return nil
			}
			return e
		}
		opened, e := openArtifact(jid, f)
		if e != nil {
			return nil
		}
		opened.Close()
	}
	_, e := pool.Exec(contextBG(), "UPDATE jobs SET legacy_fulfilled=true WHERE id=$1 AND NOT refunded", jid)
	return e
}

func artifactKind(kind string, idx int) string {
	if kind == "board" {
		return legacyBoardKind(idx)
	}
	if kind == "system" {
		return "system_card"
	}
	return kind
}
