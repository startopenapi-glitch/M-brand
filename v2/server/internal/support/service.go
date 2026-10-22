package support

import (
	"context"
	"errors"
	"strings"

	"github.com/startopenapi-glitch/M-brand/v2/server/internal/domain/support"
	"github.com/startopenapi-glitch/M-brand/v2/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("support case not found")
var ErrConflict = errors.New("support request key belongs to another visitor")
var ErrInvalid = errors.New("invalid support input")

type CreateInput struct{ RequestKey, Visitor, JobID, OrderID, Category, Description string }
type Row struct {
	ID, JobID, OrderID, Category, Description, Status, Resolution, CreatedAt string
	Version                                                                  int64
}
type ReviewInput struct{ ID, Status, Resolution, Actor, Note string; Version *int64 }
type Service struct{ DB *pgxpool.Pool }

func (s Service) Create(ctx context.Context, in CreateInput) (Row, bool, error) {
	if strings.TrimSpace(in.Description) == "" || len([]rune(in.Description)) > 2000 || len(in.RequestKey) < 8 || len(in.RequestKey) > 100 {
		return Row{}, false, ErrInvalid
	}
	if in.JobID == "" && in.OrderID == "" {
		return Row{}, false, ErrInvalid
	}
	var owned int
	if in.JobID != "" {
		if s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM jobs WHERE id=$1 AND visitor=$2`, in.JobID, in.Visitor).Scan(&owned) != nil || owned == 0 {
			return Row{}, false, ErrNotFound
		}
	}
	if in.OrderID != "" {
		if s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM orders WHERE id=$1 AND visitor=$2`, in.OrderID, in.Visitor).Scan(&owned) != nil || owned == 0 {
			return Row{}, false, ErrNotFound
		}
	}
	cat := in.Category
	if cat == "" {
		cat = "order"
	}
	if cat != "order" && cat != "generation" && cat != "payment" && cat != "other" {
		cat = "other"
	}
	var out Row
	var owner string
	var inserted bool
	err := s.DB.QueryRow(ctx, `INSERT INTO support_cases(id,request_key,visitor,job_id,order_id,category,description)
		VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,$7)
		ON CONFLICT (request_key) DO UPDATE SET request_key=EXCLUDED.request_key
		RETURNING id,status,COALESCE(resolution,''),COALESCE(to_char(created_at,'YYYY-MM-DD HH24:MI'),''),visitor,(xmax=0)`, platform.NewID(12), in.RequestKey, in.Visitor, in.JobID, in.OrderID, cat, strings.TrimSpace(in.Description)).Scan(&out.ID, &out.Status, &out.Resolution, &out.CreatedAt, &owner, &inserted)
	if err != nil {
		return Row{}, false, err
	}
	if owner != in.Visitor {
		return Row{}, false, ErrConflict
	}
	if inserted {
		_, _ = s.DB.Exec(ctx, `INSERT INTO support_events(case_id,actor,action,note) VALUES($1,'customer','created',$2)`, out.ID, strings.TrimSpace(in.Description))
	}
	return out, inserted, nil
}

func (s Service) List(ctx context.Context, visitor string) ([]Row, error) {
	rows, err := s.DB.Query(ctx, `SELECT id,COALESCE(job_id,''),COALESCE(order_id,''),category,description,status,resolution,to_char(created_at,'YYYY-MM-DD HH24:MI') FROM support_cases WHERE visitor=$1 ORDER BY created_at DESC LIMIT 50`, visitor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		if rows.Scan(&r.ID, &r.JobID, &r.OrderID, &r.Category, &r.Description, &r.Status, &r.Resolution, &r.CreatedAt) == nil {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

func (s Service) AdminList(ctx context.Context, orderIDs ...string) ([]Row, error) {
	orderID:="";if len(orderIDs)>0{orderID=orderIDs[0]}
	rows, err := s.DB.Query(ctx, `SELECT id,COALESCE(job_id,''),COALESCE(order_id,''),category,description,status,resolution,to_char(created_at,'YYYY-MM-DD HH24:MI'),version FROM support_cases WHERE ($1='' OR order_id=$1) ORDER BY CASE status WHEN 'open' THEN 0 WHEN 'reviewing' THEN 1 ELSE 2 END,created_at DESC LIMIT 200`,orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		if rows.Scan(&r.ID, &r.JobID, &r.OrderID, &r.Category, &r.Description, &r.Status, &r.Resolution, &r.CreatedAt, &r.Version) == nil {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

func (s Service) Review(ctx context.Context, in ReviewInput) (Row, error) {
	if in.Status == "" {
		in.Status = "reviewing"
	}
	if !support.ValidStatus(in.Status) {
		return Row{}, ErrInvalid
	}

 tx,err:=s.DB.Begin(ctx);if err!=nil{return Row{},err};defer tx.Rollback(ctx)
 var current int64
 if err=tx.QueryRow(ctx,"SELECT version FROM support_cases WHERE id=$1 FOR UPDATE",in.ID).Scan(&current);errors.Is(err,pgx.ErrNoRows){return Row{},ErrNotFound}else if err!=nil{return Row{},err}
 if in.Version!=nil&&*in.Version!=current{return Row{},ErrConflict}
 var out Row
 err=tx.QueryRow(ctx,`UPDATE support_cases SET status=$2,resolution=$3,version=version+1,updated_at=now() WHERE id=$1 RETURNING id,version`,in.ID,in.Status,strings.TrimSpace(in.Resolution)).Scan(&out.ID,&out.Version)
 if err!=nil{return Row{},err}
 actor:=in.Actor;if actor==""{actor="admin"}
 if _,err=tx.Exec(ctx,`INSERT INTO support_events(case_id,actor,action,note) VALUES($1,$2,$3,$4)`,out.ID,actor,in.Status,strings.TrimSpace(in.Note+" "+in.Resolution));err!=nil{return Row{},err}
 out.Status=in.Status
 return out,tx.Commit(ctx)
}
