package app

// Runtime health: API process liveness is separate from worker liveness.
// A worker that stops heartbeating makes /api/health fail closed so a release
// or uptime monitor cannot mistake a running API for a working generation queue.

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

const workerHeartbeatStale = 35 * time.Second

func startWorkerHeartbeat() {
	instance := newID(8)
	beat := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var queue, running int
		_ = pool.QueryRow(ctx, `SELECT COUNT(*) FILTER (WHERE status='queued'),COUNT(*) FILTER (WHERE status='running') FROM jobs`).Scan(&queue, &running)
		details, _ := json.Marshal(map[string]int{"queued": queue, "running": running})
		_, err := pool.Exec(ctx, `INSERT INTO worker_heartbeats(worker,instance_id,last_seen,details)
			VALUES('generation',$1,now(),$2::jsonb)
			ON CONFLICT (worker) DO UPDATE SET instance_id=EXCLUDED.instance_id,last_seen=now(),details=EXCLUDED.details`, instance, string(details))
		if err != nil {
			slog.Warn("worker heartbeat failed", "err", err)
		}
	}
	beat()
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for range t.C {
			beat()
		}
	}()
}

func workerHealthSnapshot() gin.H {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var instance string
	var last time.Time
	var raw []byte
	err := pool.QueryRow(ctx, `SELECT instance_id,last_seen,details FROM worker_heartbeats WHERE worker='generation'`).Scan(&instance, &last, &raw)
	if err != nil {
		return gin.H{"ready": false, "error": "worker heartbeat missing"}
	}
	details := map[string]int{}
	_ = json.Unmarshal(raw, &details)
	return gin.H{
		"ready":       last.After(time.Now().Add(-workerHeartbeatStale)),
		"instance":    instance,
		"last_seen":   last.UTC().Format(time.RFC3339),
		"age_seconds": int(time.Since(last).Seconds()),
		"queued":      details["queued"],
		"running":     details["running"],
	}
}
