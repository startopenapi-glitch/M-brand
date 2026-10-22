package platform

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OpenDB owns connection creation and ping policy. Migration policy stays in
// the application because it is a deployment concern.
func OpenDB(ctx context.Context, url string) (*pgxpool.Pool, error) {
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}
