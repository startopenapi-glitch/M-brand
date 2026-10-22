// Package analytics records privacy-preserving funnel events.
package analytics

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
)

type Execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}
type Service struct{ DB Execer }

func (s Service) Record(ctx context.Context, visitorHash, event, jobID, path, agentCode string) error {
	if visitorHash == "" {
		return nil
	}
	_, err := s.DB.Exec(ctx, `INSERT INTO analytics_events(event,visitor_hash,job_id,path,agent_code) VALUES($1,$2,$3,$4,$5)`, event, visitorHash, jobID, path, agentCode)
	return err
}
