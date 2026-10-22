package app

// Independent flow review 2026-10-20. Test DB only; no worker, external HTTP,
// real payment or generation is started. Every fixture has t.Cleanup coverage.
import (
 "archive/zip"
 "bytes"
 "encoding/json"
 "io"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "sync"
 "testing"
)

func reviewFlowCreate(t *testing.T, vid,key,name string) *httptest.ResponseRecorder {
 t.Helper()
 req:=httptest.NewRequest("POST","/0brand/api/jobs",strings.NewReader(`{"cn_name":"`+name+`","industry":"测试"}`))
 req.Header.Set("Content-Type","application/json");req.Header.Set("Idempotency-Key",key)
 req.AddCookie(commercialCookie(visitorCookie,vid,0));r:=httptest.NewRecorder();buildRouter().ServeHTTP(r,req);return r
}
func reviewFlowVisitor(t *testing.T) string {
 t.Helper();commercialRequireTestDB(t);resetCreateLimiter();t.Cleanup(resetCreateLimiter)
 vid:="review-flow-"+randHex(8);mustExec(t,"INSERT INTO visitors(id) VALUES($1)",vid)
 t.Cleanup(func(){mustExec(t,"DELETE FROM audit_events WHERE actor=$1","visitor:"+vid);mustExec(t,"DELETE FROM jobs WHERE visitor=$1",vid);mustExec(t,"DELETE FROM visitors WHERE id=$1",vid)})
 return vid
}
func reviewFlowCreateID(t *testing.T,r *httptest.ResponseRecorder) string {
 t.Helper();if r.Code!=200&&r.Code!=201 {t.Fatalf("create HTTP %d: %s",r.Code,r.Body)}
 var out struct{ID string `json:"id"`};if err:=json.Unmarshal(r.Body.Bytes(),&out);err!=nil||out.ID==""{t.Fatalf("invalid response: %s",r.Body)};return out.ID
}
func TestReviewFlowCreateReplayReturnsOriginalJob(t *testing.T) {
 vid:=reviewFlowVisitor(t);key:="review-create-"+randHex(12)
 first:=reviewFlowCreateID(t,reviewFlowCreate(t,vid,key,"相同意图"))
 // Discarding the first response models a lost response followed by page reload.
 second:=reviewFlowCreateID(t,reviewFlowCreate(t,vid,key,"相同意图"))
 var n int;if err:=pool.QueryRow(contextBG(),"SELECT COUNT(*) FROM jobs WHERE visitor=$1",vid).Scan(&n);err!=nil{t.Fatal(err)}
 if first!=second||n!=1 {t.Fatalf("one intention created duplicates: first=%s second=%s job_count=%d",first,second,n)}
}
func TestReviewFlowCreateConcurrentReplayReturnsOneJob(t *testing.T) {
 vid:=reviewFlowVisitor(t);key:="review-concurrent-"+randHex(12)
 var wg sync.WaitGroup;responses:=make(chan *httptest.ResponseRecorder,3)
 for i:=0;i<3;i++ {wg.Add(1);go func(){defer wg.Done();responses<-reviewFlowCreate(t,vid,key,"并发意图")}()};wg.Wait();close(responses)
 ids:=map[string]bool{};for r:=range responses {ids[reviewFlowCreateID(t,r)]=true}
 var n int;if err:=pool.QueryRow(contextBG(),"SELECT COUNT(*) FROM jobs WHERE visitor=$1",vid).Scan(&n);err!=nil{t.Fatal(err)}
 if len(ids)!=1||n!=1 {t.Fatalf("concurrent replay created %d response IDs / %d jobs",len(ids),n)}
}
func TestReviewFlowCreateKeyCannotChangeForm(t *testing.T) {
 vid:=reviewFlowVisitor(t);key:="review-mismatch-"+randHex(12)
 reviewFlowCreateID(t,reviewFlowCreate(t,vid,key,"原品牌"))
 r:=reviewFlowCreate(t,vid,key,"不同品牌")
 if r.Code!=409 {t.Fatalf("same key with changed intent must conflict: HTTP %d %s",r.Code,r.Body)}
}
func TestReviewFlowChooseConcurrentConsumesOnce(t *testing.T) {
 jid,vid:=commercialFixture(t,"ready_to_choose","choose");code:=upperCode("FLOWCHOOSE")
 mustExec(t,"INSERT INTO invite_codes(code,max_uses,bound_visitor) VALUES($1,1,$2)",code,vid)
 t.Cleanup(func(){mustExec(t,"DELETE FROM invite_codes WHERE code=$1",code)})
 mustExec(t,"UPDATE jobs SET code=$2 WHERE id=$1",jid,code)
 var wg sync.WaitGroup;responses:=make(chan *httptest.ResponseRecorder,6)
 for i:=0;i<6;i++ {wg.Add(1);go func(){defer wg.Done();responses<-commerceRequest(t,"POST","/0brand/api/jobs/"+jid+"/choose",vid,`{"direction":2}`)}()};wg.Wait();close(responses)
 for r:=range responses {if r.Code!=200 {t.Fatalf("choose HTTP %d %s",r.Code,r.Body)}}
 var used,chosen int;var status,stage string
 if err:=pool.QueryRow(contextBG(),"SELECT used FROM invite_codes WHERE code=$1",code).Scan(&used);err!=nil{t.Fatal(err)}
 if err:=pool.QueryRow(contextBG(),"SELECT chosen,status,stage FROM jobs WHERE id=$1",jid).Scan(&chosen,&status,&stage);err!=nil{t.Fatal(err)}
 if used!=1||chosen!=2||status!="queued"||(stage!="boards"&&stage!="logo") {t.Fatalf("choice replay used=%d chosen=%d state=%s/%s",used,chosen,status,stage)}
}
func TestReviewFlowDownloadsOnlyPurchasedOriginals(t *testing.T) {
 j:=commercialJob(t,"done","done",1);vid:="review-download-"+randHex(8)
 mustExec(t,"UPDATE jobs SET visitor=$2 WHERE id=$1",j.ID,vid)
 grantTestItems(t,j.ID,"logo_h","brand_story")
 raw:=commercialPNG(t,100);commercialWritePNG(t,j.ID,"logo_h.png",raw);commercialWritePNG(t,j.ID,"logo_v.png",commercialPNG(t,120))
 story:=[]byte("# 完整品牌故事\n这是已付费交付。\n")
 if err:=os.WriteFile(filepath.Join(jobDir(j.ID),"brand_story.md"),story,0600);err!=nil{t.Fatal(err)}
 for _,k:=range []string{"logo_h","logo_v","brand_story"} {ext:=".png";if k=="brand_story"{ext=".md"};mustExec(t,"INSERT INTO job_images(job_id,kind,idx,file,tokens) VALUES($1,$2,1,$3,0)",j.ID,k,webPath(j.ID,k+ext))}
 for _,tc:=range []struct{vid,path string;status int}{{vid,"logo_h.png",200},{vid,"logo_v.png",404},{"other-visitor","logo_h.png",404},{vid,"brand_story.md",200}} {
  r:=commerceRequest(t,"GET","/0brand/gen/"+j.ID+"/"+tc.path,tc.vid,"");if r.Code!=tc.status{t.Fatalf("single download %s owner=%t HTTP %d",tc.path,tc.vid==vid,r.Code)}
 }
 r:=commerceRequest(t,"GET","/0brand/api/jobs/"+j.ID+"/download",vid,"");if r.Code!=200{t.Fatalf("ZIP HTTP %d %s",r.Code,r.Body)}
 z,err:=zip.NewReader(bytes.NewReader(r.Body.Bytes()),int64(r.Body.Len()));if err!=nil{t.Fatal(err)}
 expected:=map[string][]byte{"logo_h.png":raw,"brand_story.md":story};if len(z.File)!=len(expected){t.Fatalf("ZIP includes wrong count: %d",len(z.File))}
 for _,f:=range z.File {in,err:=f.Open();if err!=nil{t.Fatal(err)};content,err:=io.ReadAll(in);in.Close();if err!=nil{t.Fatal(err)};want,ok:=expected[f.Name];if !ok||!bytes.Equal(content,want){t.Fatalf("ZIP wrong entry/content: %s",f.Name)}}
 r=commerceRequest(t,"GET","/0brand/api/jobs/"+j.ID+"/download","other-visitor","");if r.Code!=404{t.Fatalf("foreign ZIP HTTP %d",r.Code)}
 if err:=os.Remove(filepath.Join(jobDir(j.ID),"logo_h.png"));err!=nil{t.Fatal(err)}
 r=commerceRequest(t,"GET","/0brand/api/jobs/"+j.ID+"/download",vid,"");if r.Code!=409||strings.Contains(r.Header().Get("Content-Type"),"zip"){t.Fatalf("missing disk artifact masquerades as complete ZIP: HTTP %d %s",r.Code,r.Body)}
}

func TestReviewFlowReplaySurvivesRateLimitAndRecovery(t *testing.T) {
 vid:=reviewFlowVisitor(t); key:="review-recover-"+randHex(12)
 id:=reviewFlowCreateID(t,reviewFlowCreate(t,vid,key,"恢复"))
 // Exhaust the creation limit with new intentions, then replay the first one.
 reviewFlowCreateID(t,reviewFlowCreate(t,vid,key+"a","恢复"));reviewFlowCreateID(t,reviewFlowCreate(t,vid,key+"b","恢复"))
 if r:=reviewFlowCreate(t,vid,key+"c","恢复");r.Code!=429{t.Fatalf("new creation must retain rate limit: %d",r.Code)}
 if replay:=reviewFlowCreateID(t,reviewFlowCreate(t,vid,key,"恢复"));replay!=id{t.Fatal("rate limit lost original job")}
 other:=reviewFlowVisitor(t)
 mustExec(t,"UPDATE jobs SET visitor=$2 WHERE id=$1",id,other)
 // The original creator cannot recreate or learn the recovered owner's job.
 r:=reviewFlowCreate(t,vid,key,"恢复");if r.Code!=409||strings.Contains(r.Body.String(),id){t.Fatalf("old device replay after recovery: %d %s",r.Code,r.Body)}
 var n int;pool.QueryRow(contextBG(),"SELECT count(*) FROM jobs WHERE creation_owner=$1",vid).Scan(&n);if n!=3{t.Fatalf("recovery created extra job: %d",n)}
 // Identical keys from another established session remain independent.
 second:=reviewFlowCreateID(t,reviewFlowCreate(t,other,key,"恢复"));if second==id{t.Fatal("creation key leaked across owners")}
}
func TestReviewFlowCreationRequiresSessionAndValidKey(t *testing.T) {
 vid:=reviewFlowVisitor(t)
 if r:=reviewFlowCreate(t,vid,"bad","无效编号");r.Code!=400{t.Fatalf("invalid key %d",r.Code)}
 req:=httptest.NewRequest("POST","/0brand/api/jobs",strings.NewReader(`{"cn_name":"无会话","industry":"测试"}`));req.Header.Set("Content-Type","application/json");req.Header.Set("Idempotency-Key","review-no-session-12345")
 r:=httptest.NewRecorder();buildRouter().ServeHTTP(r,req);if r.Code!=428{t.Fatalf("creation before session handshake %d",r.Code)}
}
