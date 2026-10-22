package app

import (
	"testing"
)

func TestInviteCodeGrantsOnlyLogoIconForNewJobs(t *testing.T) {
	code := "LOGOICON" + newID(8)
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used,bound_visitor,item_kind) VALUES($1,1,1,$2,'logo_icon')", code, "icon-owner")
	t.Cleanup(func() { mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code) })
	vid := "icon-job-" + newID(10)
	mustExec(t, "INSERT INTO visitors(id) VALUES($1)", vid)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM jobs WHERE visitor=$1", vid)
		mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
	})
	jid := "ICON" + newID(10)
	mustExec(t, "INSERT INTO jobs(id,code,form,status,stage,visitor,quota_code,legacy_code_rights) VALUES($1,'','{}','ready_to_confirm','logo',$2,$3,false)", jid, vid, code)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM job_images WHERE job_id=$1", jid)
		mustExec(t, "DELETE FROM delivery_items WHERE job_id=$1", jid)
		mustExec(t, "DELETE FROM order_items WHERE order_id IN (SELECT id FROM orders WHERE job_id=$1)", jid)
		mustExec(t, "DELETE FROM orders WHERE job_id=$1", jid)
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
	})
	rights, err := jobEntitlements(contextBG(), pool, jid)
	if err != nil {
		t.Fatal(err)
	}
	if !rights["logo_icon"] || rights["worldview"] || rights["logo_h"] || rights["color_typo"] {
		t.Fatalf("new code must grant only logo_icon: %v", rights)
	}
}

func TestLegacyJobKeepsHistoricalCodeRights(t *testing.T) {
	code := "LEGACY" + newID(8)
	mustExec(t, "INSERT INTO invite_codes(code,max_uses,used,bound_visitor) VALUES($1,1,1,$2)", code, "legacy-owner")
	t.Cleanup(func() { mustExec(t, "DELETE FROM invite_codes WHERE code=$1", code) })
	vid := "legacy-job-" + newID(10)
	mustExec(t, "INSERT INTO visitors(id) VALUES($1)", vid)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM jobs WHERE visitor=$1", vid)
		mustExec(t, "DELETE FROM visitors WHERE id=$1", vid)
	})
	jid := "LEG" + newID(10)
	mustExec(t, "INSERT INTO jobs(id,code,form,status,stage,visitor,quota_code) VALUES($1,'','{}','done','done',$2,$3)", jid, vid, code)
	t.Cleanup(func() {
		mustExec(t, "DELETE FROM job_images WHERE job_id=$1", jid)
		mustExec(t, "DELETE FROM delivery_items WHERE job_id=$1", jid)
		mustExec(t, "DELETE FROM jobs WHERE id=$1", jid)
	})
	rights, err := jobEntitlements(contextBG(), pool, jid)
	if err != nil {
		t.Fatal(err)
	}
	if !rights["worldview"] || !rights["logo_logic"] {
		t.Fatalf("historical job rights lost: %v", rights)
	}
}
