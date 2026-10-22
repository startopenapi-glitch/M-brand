// Package adminorders provides read-only order queries, independent of HTTP and settlement.
package adminorders

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Filter struct {
	Q, Status, Sort string
	IncludeMock     bool
	Page, PageSize  int
	From, To        *time.Time
}
type Row struct {
	ID          string `json:"id"`
	Cents       int64  `json:"amount_cents"`
	Channel     string `json:"channel"`
	Status      string `json:"status"`
	Credited    bool   `json:"credited"`
	Mock        bool   `json:"mock"`
	Transaction string `json:"wx_transaction_id"`
	Code        string `json:"code"`
	Visitor     string `json:"visitor"`
	Created     string `json:"created_at"`
	Paid        string `json:"paid_at"`
	JobID       string `json:"job_id"`
}
type Summary struct {
	Revenue int64 `json:"revenue_cents"`
	Paid    int64 `json:"paid"`
	Pending int64 `json:"pending"`
}
type Page struct {
	Items    []Row   `json:"items"`
	Total    int64   `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
	Summary  Summary `json:"summary"`
}

func Parse(v url.Values) (Filter, error) {
	f := Filter{Q: strings.TrimSpace(v.Get("q")), Status: v.Get("status"), Sort: v.Get("sort"), IncludeMock: v.Get("include_mock") == "1", Page: 1, PageSize: 50}
	invalid := fmt.Errorf("筛选参数无效")
	if len(f.Q) > 200 {
		return f, invalid
	}
	if f.Status == "all" {
		f.Status = ""
	}
	switch f.Status {
	case "", "paid", "pending", "expired", "closed":
	default:
		return f, invalid
	}
	if f.Sort == "" {
		f.Sort = "newest"
	}
	if f.Sort != "newest" && f.Sort != "oldest" {
		return f, invalid
	}
	for _, x := range []struct {
		k   string
		p   *int
		max int
	}{{"page", &f.Page, 100000}, {"page_size", &f.PageSize, 100}} {
		if s := v.Get(x.k); s != "" {
			n, e := strconv.Atoi(s)
			if e != nil || n < 1 || n > x.max {
				return f, invalid
			}
			*x.p = n
		}
	}
	zone, e := time.LoadLocation("Asia/Shanghai")
	if e != nil {
		return f, e
	}
	for _, x := range []struct {
		k string
		p **time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if s := v.Get(x.k); s != "" {
			t, e := time.ParseInLocation("2006-01-02", s, zone)
			if e != nil {
				return f, invalid
			}
			*x.p = &t
		}
	}
	if f.From != nil && f.To != nil && f.To.Before(*f.From) {
		return f, invalid
	}
	if f.To != nil {
		t := f.To.AddDate(0, 0, 1)
		f.To = &t
	}
	return f, nil
}

type Service struct{ DB *pgxpool.Pool }

const where = ` FROM orders o WHERE ($1='' OR o.status=$1) AND ($2 OR o.mock=false) AND ($3='' OR strpos(lower(o.id),lower($3))>0 OR strpos(lower(COALESCE(o.wx_transaction_id,'')),lower($3))>0 OR strpos(lower(COALESCE(o.visitor,'')),lower($3))>0 OR strpos(lower(COALESCE(o.code,'')),lower($3))>0) AND ($4::timestamptz IS NULL OR o.created_at >= $4) AND ($5::timestamptz IS NULL OR o.created_at < $5)`

func (s Service) Query(ctx context.Context, f Filter) (Page, error) {
	out := Page{Items: []Row{}, Page: f.Page, PageSize: f.PageSize}
	tx, e := s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	args := []any{f.Status, f.IncludeMock, f.Q, f.From, f.To}
	e = tx.QueryRow(ctx, `SELECT count(*),COALESCE(sum(amount_cents) FILTER(WHERE status='paid' AND mock=false),0),count(*) FILTER(WHERE status='paid'),count(*) FILTER(WHERE status='pending')`+where, args...).Scan(&out.Total, &out.Summary.Revenue, &out.Summary.Paid, &out.Summary.Pending)
	if e != nil {
		return out, e
	}
	order := "DESC"
	if f.Sort == "oldest" {
		order = "ASC"
	}
	rows, e := tx.Query(ctx, `SELECT o.id,o.amount_cents,o.channel,o.status,o.credited,o.mock,COALESCE(o.wx_transaction_id,''),COALESCE(o.code,''),COALESCE(o.visitor,''),COALESCE(to_char(o.created_at AT TIME ZONE 'Asia/Shanghai','YYYY-MM-DD HH24:MI:SS'),''),COALESCE(to_char(o.paid_at AT TIME ZONE 'Asia/Shanghai','YYYY-MM-DD HH24:MI:SS'),''),COALESCE(o.job_id,'')`+where+` ORDER BY o.created_at `+order+`,o.id `+order+` LIMIT $6 OFFSET $7`, append(args, f.PageSize, (f.Page-1)*f.PageSize)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var r Row
		if e = rows.Scan(&r.ID, &r.Cents, &r.Channel, &r.Status, &r.Credited, &r.Mock, &r.Transaction, &r.Code, &r.Visitor, &r.Created, &r.Paid, &r.JobID); e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit(ctx)
}
