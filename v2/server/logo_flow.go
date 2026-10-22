package app

// Formal Logo review, confirmation and revision. The direction card is an
// exploration; this stage produces the separate Logo master candidate. Paid
// applications only start after the customer confirms it.
import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func stageLogo(j jobRow) error {
	brief := loadBrandBrief(j)
	dirs := loadLogoDirections(j)
	if j.Chosen == nil || *j.Chosen < 0 || *j.Chosen >= len(dirs) {
		return fmt.Errorf("缺少已选方向")
	}
	d := dirs[*j.Chosen]
	prompt := buildFormalLogoPrompt(brief, d)
	name := "system_card.png"
	if err := ensureJobImageSize(j, "system_card", 0, name, prompt, nil, "1024x1280"); err != nil {
		recordGeneration(j.ID, "formal_logo", "system_card", 0, "failed", prompt, imgConf().model, "", "", nil, "", "", err.Error(), 0)
		return err
	}
	recordGeneration(j.ID, "formal_logo", "system_card", 0, "succeeded", prompt, imgConf().model, "", "", nil, webPath(j.ID, name), "", "", 0)
	_, _ = pool.Exec(contextBG(), `INSERT INTO logo_versions(job_id,version,file,prompt,parent_file,instruction,direction_snapshot,status)
		VALUES($1,1,$2,$3,'','',$4,'succeeded') ON CONFLICT(job_id,version) DO NOTHING`,
		j.ID, webPath(j.ID, name), prompt, mustJSON(d))
	return setExecutionState(j, "ready_to_confirm", "logo")
}

func logoVersionsCount(jid string) int {
	var n int
	_ = pool.QueryRow(contextBG(), "SELECT COUNT(*) FROM logo_versions WHERE job_id=$1", jid).Scan(&n)
	return n
}

func stageLogoRevision(j jobRow) error {
	instruction := j.LogoRevisionInstruction
	if instruction == "" {
		var q string
		_ = pool.QueryRow(contextBG(), "SELECT logo_revision_instruction FROM jobs WHERE id=$1", j.ID).Scan(&q)
		instruction = q
	}
	if instruction == "" {
		return fmt.Errorf("缺少修改要求")
	}
	brief := loadBrandBrief(j)
	dirs := loadLogoDirections(j)
	if j.Chosen == nil || *j.Chosen < 0 || *j.Chosen >= len(dirs) {
		return fmt.Errorf("缺少已选方向")
	}
	d := dirs[*j.Chosen]
	current := filepath.Join(jobDir(j.ID), "system_card.png")
	if !fileExists(current) {
		return fmt.Errorf("正式 Logo 不存在")
	}
	// Keep the pre-revision image as an immutable version.
	version := logoVersionsCount(j.ID) + 2
	oldVersion := filepath.Join(jobDir(j.ID), fmt.Sprintf("logo_v%d.png", version-1))
	if !fileExists(oldVersion) {
		raw, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		if err := os.WriteFile(oldVersion, raw, 0o644); err != nil {
			return err
		}
	}
	keep := []string{"未在修改要求中明确改变的部分", "正式 Logo 的准确字标「" + brief.ExactWordmark + "」"}
	change := []string{instruction}
	prompt := buildConstrainedRevisionPrompt(brief, d, instruction, keep, change)
	tmp := filepath.Join(jobDir(j.ID), "logo_revision_tmp.png")
	refs := []string{current}
	if j.Chosen != nil {
		dirPath := filepath.Join(jobDir(j.ID), fmt.Sprintf("direction_%d.png", *j.Chosen))
		if fileExists(dirPath) {
			refs = append(refs, dirPath)
		}
	}
	started := time.Now()
	r, err := GenerateSize(prompt, tmp, refs, "1024x1280")
	if err != nil {
		recordGeneration(j.ID, "logo_revision", "system_card", 0, "failed", prompt, imgConf().model, "", "system_card.png", []referenceImage{{Path: current, Role: "logo"}, {Path: refs[len(refs)-1], Role: "direction"}}, "", "", err.Error(), 0)
		return err
	}
	raw, err := os.ReadFile(tmp)
	if err != nil {
		return err
	}
	if err := writeImageFile(filepath.Join(jobDir(j.ID), fmt.Sprintf("logo_v%d.png", version)), raw); err != nil {
		return err
	}
	if err := writeImageFile(current, raw); err != nil {
		return err
	}
	_ = os.Remove(tmp)
	if err := recordImage(j.ID, j.Code, "system_card", 0, webPath(j.ID, "system_card.png"), r.Tokens, prompt, j.ExecutionToken); err != nil {
		return err
	}
	_, _ = pool.Exec(contextBG(), `INSERT INTO logo_versions(job_id,version,file,prompt,parent_file,instruction,direction_snapshot,status)
		VALUES($1,$2,$3,$4,$5,$6,$7,'succeeded') ON CONFLICT(job_id,version) DO UPDATE SET file=EXCLUDED.file,prompt=EXCLUDED.prompt,instruction=EXCLUDED.instruction`,
		j.ID, version, webPath(j.ID, fmt.Sprintf("logo_v%d.png", version)), prompt, webPath(j.ID, fmt.Sprintf("logo_v%d.png", version-1)), instruction, mustJSON(d))
	recordGeneration(j.ID, "logo_revision", "system_card", version, "succeeded", prompt, imgConf().model, "", "system_card.png", []referenceImage{{Path: current, Role: "logo"}}, webPath(j.ID, "system_card.png"), "", "", r.Tokens)
	if err := saveApprovedRevisionConstraints(j, brief, d, instruction); err != nil { return err }
	slog.Info("logo revision generated", "job", j.ID, "version", version, "ms", time.Since(started).Milliseconds(), "tokens", r.Tokens)
	return setExecutionState(j, "ready_to_confirm", "logo")
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func slogInfo(msg string, args ...any) { slog.Info(msg, args...) }

// Confirm the formal Logo, then queue the paid-application stage. No rights or
// prices are changed here.
func confirmLogo(c *gin.Context) {
	jid := c.Param("id")
	vid := visitorID(c)
	gateCode := ""
	if gate := gateOK(c); gate != nil {
		gateCode = gate.Code
	}
	var status, stage, owner, code string
	if err := pool.QueryRow(c, "SELECT status,stage,COALESCE(visitor,''),COALESCE(NULLIF(quota_code,''),code,'') FROM jobs WHERE id=$1", jid).Scan(&status, &stage, &owner, &code); err != nil {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	if !jobOwned(c, vid, gateCode, owner, code) {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	if stage == "boards" || stage == "done" {
		c.JSON(200, gin.H{"ok": true, "reused": true})
		return
	}
	if status != "ready_to_confirm" || stage != "logo" {
		c.JSON(409, gin.H{"error": "Logo 还未准备好"})
		return
	}
	tag, err := pool.Exec(c, "UPDATE jobs SET logo_confirmed=true,stage='boards',status='queued',error='',updated_at=now() WHERE id=$1 AND status='ready_to_confirm' AND stage='logo'", jid)
	if err != nil || tag.RowsAffected() != 1 {
		c.JSON(409, gin.H{"error": "当前状态不可确认"})
		return
	}
	c.JSON(200, gin.H{"ok": true, "status": "queued"})
}

func reviseLogo(c *gin.Context) {
	jid := c.Param("id")
	var req struct {
		Instruction string `json:"instruction"`
	}
	if c.BindJSON(&req) != nil {
		c.JSON(400, gin.H{"error": "参数无效"})
		return
	}
	req.Instruction = strings.TrimSpace(req.Instruction)
	if req.Instruction == "" || len([]rune(req.Instruction)) > 300 {
		c.JSON(400, gin.H{"error": "请输入 300 字以内的修改要求"})
		return
	}
	vid := visitorID(c)
	gateCode := ""
	if gate := gateOK(c); gate != nil {
		gateCode = gate.Code
	}
	var status, stage, owner, code string
	if err := pool.QueryRow(c, "SELECT status,stage,COALESCE(visitor,''),COALESCE(NULLIF(quota_code,''),code,'') FROM jobs WHERE id=$1", jid).Scan(&status, &stage, &owner, &code); err != nil {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	if !jobOwned(c, vid, gateCode, owner, code) {
		c.JSON(404, gin.H{"error": "任务不存在"})
		return
	}
	if status != "ready_to_confirm" || stage != "logo" {
		c.JSON(409, gin.H{"error": "当前状态不可修改"})
		return
	}
	if _, err := pool.Exec(c, "UPDATE jobs SET logo_revision_instruction=$2,stage='logo_revision',status='queued',error='',updated_at=now() WHERE id=$1 AND status='ready_to_confirm' AND stage='logo'", jid, req.Instruction); err != nil {
		c.JSON(500, gin.H{"error": "系统错误"})
		return
	}
	c.JSON(200, gin.H{"ok": true, "status": "queued"})
}
