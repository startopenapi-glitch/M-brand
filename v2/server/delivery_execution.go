package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

var errGenerationUnknown = errors.New("生成结果待核对，已完成内容和已购权益已保留，请联系支持")
var errExecutionLost = errors.New("任务已由其他执行者接管")

// Only a confirmed missing row is a 404. Infrastructure errors must not erase
// client recovery identities or masquerade as an authorization decision.
func readFailure(c *gin.Context, err error, missing string) {
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(404, gin.H{"error": missing})
		return
	}
	c.Header("Retry-After", "3")
	c.JSON(503, gin.H{"error": "服务暂时不可用，已保留原任务和订单，请稍后重试"})
}

// Session advisory locks prove live ownership across DB recovery and processes.
// Unlike elapsed-time-only leases, a slow healthy worker cannot be stolen.
func recoverInterruptedJobs() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, e := pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	rows, e := tx.Query(ctx, `SELECT id FROM jobs WHERE status IN ('running','boards_running') ORDER BY updated_at LIMIT 100 FOR UPDATE SKIP LOCKED`)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			break
		}
		ids = append(ids, id)
	}
	re := rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if re != nil {
		return re
	}
	for _, id := range ids {
		var free bool
		if e = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(197705,hashtext($1))", id).Scan(&free); e != nil {
			return e
		}
		if free {
			if _, e = tx.Exec(ctx, "UPDATE jobs SET status='queued',execution_token='',updated_at=now() WHERE id=$1", id); e != nil {
				return e
			}
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	return recoverLateArtifacts(ctx)
}

// An unknown attempt can resume only with a complete artifact already on disk.
func recoverLateArtifacts(ctx context.Context) error {
	rows, e := pool.Query(ctx, `WITH batch AS (
 SELECT ga.job_id,ga.kind,ga.idx FROM generation_attempts ga JOIN jobs j ON j.id=ga.job_id
 WHERE j.status='error' AND ga.state IN ('submitted','unknown')
 ORDER BY ga.last_checked_at NULLS FIRST,ga.job_id,ga.kind,ga.idx LIMIT 100 FOR UPDATE OF ga SKIP LOCKED
 ) UPDATE generation_attempts ga SET last_checked_at=now() FROM batch b
 WHERE ga.job_id=b.job_id AND ga.kind=b.kind AND ga.idx=b.idx RETURNING ga.job_id,ga.kind,ga.idx`)
	if e != nil {
		return e
	}
	type pending struct {
		id, kind string
		idx      int
	}
	var items []pending
	for rows.Next() {
		var p pending
		if e = rows.Scan(&p.id, &p.kind, &p.idx); e != nil {
			break
		}
		items = append(items, p)
	}
	re := rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if re != nil {
		return re
	}
	for _, p := range items {
		name := p.kind + ".png"
		if p.kind == "direction" {
			name = fmt.Sprintf("direction_%d.png", p.idx)
		}
		f, e := openArtifact(p.id, webPath(p.id, name))
		if e != nil {
			continue
		}
		st, e := f.Stat()
		f.Close()
		if e != nil || st.Size() > maxImageBytes {
			continue
		}
		raw, e := os.ReadFile(filepath.Join(jobDir(p.id), name))
		if e != nil {
			continue
		}
		if _, e = imagePNG(raw); e != nil {
			continue
		}
		if _, e = pool.Exec(ctx, "UPDATE jobs SET status='queued',execution_token='',updated_at=now() WHERE id=$1 AND status='error'", p.id); e != nil {
			return e
		}
	}
	return nil
}
func releaseExecution(j jobRow) {
	if j.executionConn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Explicit unlock acknowledges normal release; closing also releases the
	// dedicated session lock when the connection has already failed.
	_,_ = j.executionConn.Exec(ctx,"SELECT pg_advisory_unlock(197705,hashtext($1))",j.ID)
	_ = j.executionConn.Close(ctx)
}
func checkExecution(j jobRow) error {
	if j.ExecutionToken == "" {
		return nil
	} // direct legacy/admin helpers; workers always carry a token
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if j.executionConn != nil {
		if e := j.executionConn.Ping(ctx); e != nil {
			return e
		}
	}
	var owned bool
	e := pool.QueryRow(ctx, "SELECT execution_token=$2 AND status IN ('running','boards_running') FROM jobs WHERE id=$1", j.ID, j.ExecutionToken).Scan(&owned)
	if e != nil {
		return e
	}
	if !owned {
		return errExecutionLost
	}
	return nil
}
func setExecutionState(j jobRow, status, stage string) error {
	if j.ExecutionToken == "" {
		return setJobState(j.ID, status, stage)
	}
	tag, e := pool.Exec(contextBG(), "UPDATE jobs SET status=$3,stage=$4,updated_at=now() WHERE id=$1 AND execution_token=$2", j.ID, j.ExecutionToken, status, stage)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return errExecutionLost
	}
	return nil
}

func startGeneration(j jobRow, kind string, idx int) error {
	if e := checkExecution(j); e != nil {
		return e
	}
	tx, e := pool.Begin(contextBG())
	if e != nil {
		return e
	}
	defer tx.Rollback(contextBG())
	var owner string
	if e = tx.QueryRow(contextBG(), "SELECT execution_token FROM jobs WHERE id=$1 FOR UPDATE", j.ID).Scan(&owner); e != nil {
		return e
	}
	if j.ExecutionToken != "" && owner != j.ExecutionToken {
		return errExecutionLost
	}
	var state string
	e = tx.QueryRow(contextBG(), "SELECT state FROM generation_attempts WHERE job_id=$1 AND kind=$2 AND idx=$3", j.ID, kind, idx).Scan(&state)
	if e != nil && e != pgx.ErrNoRows {
		return e
	}
	if e == nil && state != "failed" {
		return errGenerationUnknown
	}
	_, e = tx.Exec(contextBG(), `INSERT INTO generation_attempts(job_id,kind,idx,state) VALUES($1,$2,$3,'submitted') ON CONFLICT(job_id,kind,idx) DO UPDATE SET state='submitted',started_at=now(),finished_at=NULL`, j.ID, kind, idx)
	if e != nil {
		return e
	}
	// A lost commit acknowledgement does not authorize sending an image request.
	return tx.Commit(contextBG())
}
func finishGeneration(j jobRow, kind string, idx int, state string) error {
	_, e := pool.Exec(contextBG(), `UPDATE generation_attempts SET state=$4,finished_at=now() WHERE job_id=$1 AND kind=$2 AND idx=$3 AND ($5='' OR EXISTS(SELECT 1 FROM jobs WHERE id=$1 AND execution_token=$5))`, j.ID, kind, idx, state, j.ExecutionToken)
	return e
}
func generationNeedsReview(jid string) (bool, error) {
	var pending bool
	e := pool.QueryRow(contextBG(), "SELECT EXISTS(SELECT 1 FROM generation_attempts WHERE job_id=$1 AND state IN ('submitted','unknown'))", jid).Scan(&pending)
	return pending, e
}

type deliveryStep struct {
	Kind       string     `json:"kind"`
	Index      int        `json:"index"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}
type deliveryProgress struct {
	StartedAt   *time.Time     `json:"started_at"`
	ObservedAt  time.Time      `json:"observed_at"`
	Steps       []deliveryStep `json:"steps"`
	NeedsReview bool           `json:"needs_review"`
}

func readDeliveryProgress(ctx context.Context, jid string) (deliveryProgress, error) {
	p := deliveryProgress{ObservedAt: time.Now().UTC(), Steps: []deliveryStep{}}
	if e := pool.QueryRow(ctx, "SELECT execution_started_at FROM jobs WHERE id=$1", jid).Scan(&p.StartedAt); e != nil {
		return p, e
	}
	rows, e := pool.Query(ctx, "SELECT kind,idx,state,started_at,finished_at FROM generation_attempts WHERE job_id=$1 ORDER BY started_at", jid)
	if e != nil {
		return p, e
	}
	defer rows.Close()
	for rows.Next() {
		var s deliveryStep
		if e = rows.Scan(&s.Kind, &s.Index, &s.State, &s.StartedAt, &s.FinishedAt); e != nil {
			return p, e
		}
		p.Steps = append(p.Steps, s)
		if s.State == "unknown" {
			p.NeedsReview = true
		}
	}
	if e = rows.Err(); e != nil {
		return p, e
	}
	return p, nil
}
func unknownGenerationError(e error) error { return fmt.Errorf("%w: %v", errGenerationUnknown, e) }
