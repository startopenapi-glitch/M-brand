package app

// jobs.go — 任务引擎：PG 任务表 + 3 并发 worker + 重启恢复

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const maxImagesPerJob = 21 // 3 方向 + 系统卡 + 4 图

type jobRow struct {
	ID, Code, Status, Stage, Error string
	DeliveryVersion                int64
	ExecutionToken                 string
	executionConn                  *pgx.Conn
	Form                           BrandForm
	Chosen                         *int
	DirectionSnapshot              *SelectedDirectionSnapshot
	LogoRevisionInstruction        string
}

type imgRecord struct {
	Kind   string `json:"kind"`
	Idx    int    `json:"idx"`
	File   string `json:"file"` // 相对 URL，如 gen/{job}/direction_0.png；为空表示失败
	Tokens int    `json:"tokens"`
}

// startWorkers 启动 worker 池；启动恢复失败时先重试，避免遗漏已占票任务。
func startWorkers(n int) {
	startLogoReferenceCleanup()
	for {
		if err := recoverInterruptedJobs(); err == nil {
			break
		} else {
			slog.Error("job recovery failed", "err", err)
			time.Sleep(1500 * time.Millisecond)
		}
	}
	go func() {
		for {
			time.Sleep(5 * time.Second)
			if e := recoverInterruptedJobs(); e != nil {
				slog.Warn("delivery recovery unavailable")
			}
		}
	}()
	sem := make(chan struct{}, n)
	go func() {
		for {
			sem <- struct{}{}
			go func() {
				defer func() { <-sem }()
				runNextJob()
			}()
		}
	}()
}

func claimJob() (jobRow, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return jobRow{}, false
	}
	defer tx.Rollback(ctx)
	var j jobRow
	var formRaw []byte
	var snapshotRaw []byte
	err = tx.QueryRow(ctx, `
		SELECT id, COALESCE(code,''), status, stage, form, chosen, COALESCE(error,''), delivery_version, COALESCE(direction_snapshot,'null'::jsonb)
		FROM jobs WHERE status='queued'
		ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(
		&j.ID, &j.Code, &j.Status, &j.Stage, &formRaw, &j.Chosen, &j.Error, &j.DeliveryVersion, &snapshotRaw)
	if err != nil {
		return jobRow{}, false
	}
	json.Unmarshal(formRaw, &j.Form)
	j.DirectionSnapshot = decodeDirectionSnapshot(snapshotRaw)
	// At most one dedicated lock connection per existing worker. Long image
	// requests must not hold an application-pool slot or deadlock a small pool.
	conn, e := pgx.ConnectConfig(ctx, pool.Config().ConnConfig.Copy())
	if e != nil {
		return jobRow{}, false
	}
	j.executionConn = conn
	var locked bool
	if e = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(197705,hashtext($1))", j.ID).Scan(&locked); e != nil || !locked {
		releaseExecution(j)
		return jobRow{}, false
	}
	j.ExecutionToken = newID(32)
	if _, err := tx.Exec(ctx, `UPDATE jobs SET status='running',execution_token=$2,
 execution_started_at=CASE WHEN execution_stage<>stage OR execution_version<>delivery_version THEN now() ELSE COALESCE(execution_started_at,now()) END,
 execution_stage=stage,execution_version=delivery_version,updated_at=now() WHERE id=$1`, j.ID, j.ExecutionToken); err != nil {
		releaseExecution(j)
		return jobRow{}, false
	}
	if err := tx.Commit(ctx); err != nil {
		releaseExecution(j)
		return jobRow{}, false
	}
	return j, true
}

func runNextJob() {
	j, ok := claimJob()
	if !ok {
		time.Sleep(1500 * time.Millisecond)
		return
	}
	defer releaseExecution(j)
	slog.Info("job start", "id", j.ID, "stage", j.Stage)
	err := runJobStages(j)
	if err != nil {
		slog.Error("job failed", "id", j.ID, "err", err)
		markJobFailedOwned(j.ID, err.Error(), j.ExecutionToken, j.DeliveryVersion)
	}
}

// markJobFailed：失败收尾——已占票的任务自动返还额度（幂等：refunded 标记只返一次）
func markJobFailed(jid, errText string, workerVersion ...int64) {
	markJobFailedOwned(jid, errText, "", workerVersion...)
}
func markJobFailedOwned(jid, errText, token string, workerVersion ...int64) {
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var quota, owner string
	var refunded, fulfilled bool
	var version int64
	if tx.QueryRow(ctx,
		"SELECT COALESCE(quota_code,''), refunded,legacy_fulfilled,delivery_version,execution_token FROM jobs WHERE id=$1 FOR UPDATE", jid).
		Scan(&quota, &refunded, &fulfilled, &version, &owner) != nil {
		return
	}
	if token != "" && owner != token {
		return
	}
	next := "error"
	if len(workerVersion) > 0 && version > workerVersion[0] {
		next = "queued"
	}
	if quota != "" && !refunded && !fulfilled && next == "error" {
		// 返还额度：used 归零时同步清掉使用痕迹（一码一次：码回到可用）
		if _, e := tx.Exec(ctx,
			`UPDATE invite_codes SET
			   used=GREATEST(used-1,0),
			   used_at=CASE WHEN used<=1 THEN NULL ELSE used_at END,
			   used_by=CASE WHEN used<=1 THEN NULL ELSE used_by END
			 WHERE code=$1`, quota); e != nil {
			return
		}
		if _, e := tx.Exec(ctx, "UPDATE jobs SET refunded=true WHERE id=$1", jid); e != nil {
			return
		}
		slog.Info("quota refunded", "job", jid, "code", quota)
	}
	if _, e := tx.Exec(ctx,
		"UPDATE jobs SET status=$3, error=$2, updated_at=now() WHERE id=$1", jid, errText, next); e != nil {
		return
	}
	tx.Commit(ctx)
}

func runJobStages(j jobRow) error {
	switch j.Stage {
	case "directions":
		return stageDirections(j)
	case "logo":
		return stageLogo(j)
	case "logo_revision":
		return stageLogoRevision(j)
	case "boards":
		return stageBoardsV3(j)
	case "choose", "done", "skipped":
		return nil
	}
	return fmt.Errorf("未知阶段 %s", j.Stage)
}

func jobDir(id string) string { return filepath.Join(genDir(), id) }

func webPath(id, name string) string { return "gen/" + id + "/" + name }

func recordImage(jid, code, kind string, idx int, file string, tokens int, prompt string, executionToken ...string) error {
	file = webpify(file) // 用户侧也走 WebP（参考图机制用磁盘 PNG 路径，不受影响）
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// 锁任务后按索引更新/插入，重启恢复和补登记不会新增重复图片记录。
	var lockedID, owner string
	if err := tx.QueryRow(ctx, "SELECT id,execution_token FROM jobs WHERE id=$1 FOR UPDATE", jid).Scan(&lockedID, &owner); err != nil {
		return err
	}
	if len(executionToken) > 0 && executionToken[0] != "" && owner != executionToken[0] {
		return errExecutionLost
	}
	tag, err := tx.Exec(ctx, `UPDATE job_images SET file=$4,prompt=$5,
		tokens=CASE WHEN $6>0 THEN $6 ELSE COALESCE(tokens,0) END
		WHERE job_id=$1 AND kind=$2 AND idx=$3`, jid, kind, idx, file, prompt, tokens)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx,
			"INSERT INTO job_images(job_id,kind,idx,file,prompt,tokens) VALUES ($1,$2,$3,$4,$5,$6)",
			jid, kind, idx, file, prompt, tokens); err != nil {
			return err
		}
	}
	if tokens > 0 {
		if _, err := tx.Exec(ctx,
			"INSERT INTO usage_events(code,job_id,kind,model,tokens) VALUES ($1,$2,$3,$4,$5)",
			code, jid, kind, imgConf().model, tokens); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ensureJobImage 先使用已完整落盘的原图；数据库登记失败后重试不再付费生图。
func ensureJobImage(j jobRow, kind string, idx int, name, prompt string, refs []string) error {
	return ensureJobImageSize(j, kind, idx, name, prompt, refs, "")
}
func ensureJobImageSize(j jobRow, kind string, idx int, name, prompt string, refs []string, size string) error {
	if e := checkExecution(j); e != nil {
		return e
	}
	dest := filepath.Join(jobDir(j.ID), name)
	tokens := 0
	raw, readErr := os.ReadFile(dest)
	if readErr != nil && !os.IsNotExist(readErr) {
		return readErr
	}
	pngRaw, imageErr := imagePNG(raw)
	if readErr == nil && imageErr == nil {
		if !bytes.Equal(raw, pngRaw) {
			if err := writeImageFile(dest, pngRaw); err != nil {
				return err
			}
		}
	} else {
		if e := startGeneration(j, kind, idx); e != nil {
			return e
		}
		started := time.Now()
		r, err := GenerateSize(prompt, dest, refs, size)
		if err != nil {
			slog.Warn("image generation failed", "job", j.ID, "kind", kind, "idx", idx, "ms", time.Since(started).Milliseconds(), "err", truncate(err.Error(), 160))
			state := "failed"
			if errors.Is(err, errGenerationUnknown) {
				state = "unknown"
			}
			_ = finishGeneration(j, kind, idx, state)
			return err
		}
		tokens = r.Tokens
		recordGeneration(j.ID, "image", kind, idx, "succeeded", prompt, r.Model, r.RequestID, "", refsFromPaths(refs), webPath(j.ID, name), r.RevisedPrompt, "", tokens)
		slog.Info("image generated", "job", j.ID, "kind", kind, "idx", idx, "ms", time.Since(started).Milliseconds(), "tokens", tokens)
	}
	if err := recordImage(j.ID, j.Code, kind, idx, webPath(j.ID, name), tokens, prompt, j.ExecutionToken); err != nil {
		return fmt.Errorf("图片记录保存失败: %w", err)
	}
	return finishGeneration(j, kind, idx, "done")
}

func setJobState(jid, status, stage string) error {
	tag, err := pool.Exec(context.Background(),
		"UPDATE jobs SET status=$2, stage=$3, updated_at=now() WHERE id=$1", jid, status, stage)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("任务状态保存失败: 任务不存在")
	}
	return nil
}

func imagesOf(jid, kind string) []imgRecord {
	rows, _ := pool.Query(context.Background(),
		"SELECT kind, idx, COALESCE(file,''), COALESCE(tokens,0) FROM job_images WHERE job_id=$1 AND kind=$2 ORDER BY idx",
		jid, kind)
	defer rows.Close()
	var out []imgRecord
	for rows.Next() {
		var r imgRecord
		rows.Scan(&r.Kind, &r.Idx, &r.File, &r.Tokens)
		out = append(out, r)
	}
	return out
}

func stageDirections(j jobRow) error {
	brief := loadBrandBrief(j)
	dirs, briefErr := buildLogoDirections(context.Background(), brief)
	if briefErr != nil {
		slog.Warn("structured direction fallback", "job", j.ID, "err", truncate(briefErr.Error(), 120))
	}
	if err := persistLogoDirections(j.ID, dirs); err != nil {
		return err
	}
	refs := []string{}
	if j.Form["logo_ref"] != "" {
		p := filepath.Join(jobDir(j.ID), "logo_ref.png")
		if fileExists(p) {
			refs = []string{p}
		}
	}
	refNote := ""
	if len(refs) > 0 {
		refNote = "\n\n【客户 Logo 参考】只提取气质、图形语言和构图，不要照抄；正式标志仍以本次方向卡中的 Symbol 与字标为准。"
	}
	for i, d := range dirs {
		p := buildDirectionCardPrompt(brief, d) + refNote
		name := fmt.Sprintf("direction_%d.png", i)
		if err := ensureJobImage(j, "direction", i, name, p, refs); err != nil {
			recordGeneration(j.ID, "directions", "direction", i, "failed", p, imgConf().model, "", "", nil, "", "", err.Error(), 0)
			return fmt.Errorf("方向 %d 生成失败: %w", i+1, err)
		}
		recordGeneration(j.ID, "directions", "direction", i, "succeeded", p, imgConf().model, "", "", refsFromPaths(refs), webPath(j.ID, name), "", "", 0)
	}
	return setExecutionState(j, "ready_to_choose", "choose")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
