package app

import (
 "errors"
 "regexp"

 "github.com/gin-gonic/gin"
 "github.com/jackc/pgx/v5"
)

var creationKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// Called inside the creation transaction/lock. creation_owner is immutable;
// jobs.visitor may move when a paid order is recovered on another device.
func replayCreation(c *gin.Context, tx pgx.Tx, owner, key string, form []byte) bool {
 if key == "" { return false }
 var id, visitor, stage string
 var sameForm, trial bool
 err := tx.QueryRow(c.Request.Context(), `SELECT id,COALESCE(visitor,''),stage,trial,CASE WHEN form ? '_design_constraints_v1' THEN form=$3::jsonb
   ELSE form=($3::jsonb - '_design_constraints_v1') END
   FROM jobs WHERE creation_owner=$1 AND creation_key=$2 FOR UPDATE`,owner,key,form).Scan(&id,&visitor,&stage,&trial,&sameForm)
 if errors.Is(err,pgx.ErrNoRows) { return false }
 if err != nil { c.JSON(503,gin.H{"error":"暂时无法核对创建结果，请稍后重试"}); return true }
 if visitor != owner { c.JSON(409,gin.H{"error":"此提案已在其他设备找回，请从我的提案核对","code":"creation_transferred"}); return true }
 if !sameForm { c.JSON(409,gin.H{"error":"该创建请求已使用，请先查看原提案"}); return true }
 c.JSON(200,gin.H{"id":id,"trial":trial,"stage":stage})
 return true
}
