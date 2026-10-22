package analytics

import (
	"context"
	"github.com/jackc/pgx/v5/pgconn"
	"testing"
)

type fakeDB struct{ called bool }

func (f *fakeDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	f.called = true
	return pgconn.CommandTag{}, nil
}
func TestRecordEmptyVisitorSkips(t *testing.T) {
	db := &fakeDB{}
	s := Service{DB: db}
	if err := s.Record(context.Background(), "", "x", "", "", ""); err != nil {
		t.Fatal(err)
	}
	if db.called {
		t.Fatal("must skip empty visitor")
	}
	if err := s.Record(context.Background(), "v", "x", "", "", ""); err != nil || !db.called {
		t.Fatal("must record")
	}
}
