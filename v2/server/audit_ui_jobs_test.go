package app

// Copy into the isolated audit server directory only, then use the guarded
// zerobrand_test runner with -run '^TestAuditUIJobs'. No production APIs are used.
// These are regression assertions: the current defective snapshot should FAIL.

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuditUIJobsSystemCardPreservesChosenDirection(t *testing.T) {
	j := commercialJob(t, "running", "boards", 2)
	var selected []byte
	for i := 0; i < 3; i++ {
		raw := commercialPNG(t, byte(40+i*70))
		commercialWritePNG(t, j.ID, fmt.Sprintf("direction_%d.png", i), raw)
		if i == 2 { selected = raw }
	}
	var mu sync.Mutex
	var paths []string
	var references [][][]byte
	var sizeFields []string
	commercialGenerator(t, func(r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.URL.Path)
		var refs [][]byte
		if strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data") {
			if err := r.ParseMultipartForm(1 << 20); err != nil { t.Error(err); return }
			defer r.MultipartForm.RemoveAll()
			sizeFields = append(sizeFields, r.FormValue("size"))
			for _, headers := range r.MultipartForm.File {
				for _, header := range headers {
					f, err := header.Open(); if err != nil { t.Error(err); continue }
					raw, err := io.ReadAll(f); f.Close(); if err != nil { t.Error(err) }
					refs = append(refs, raw)
				}
			}
		} else { sizeFields = append(sizeFields, "generation-json") }
		references = append(references, refs)
	})
	if err := stageBoardsV3(j); err != nil { t.Fatal(err) }
 mu.Lock()
 if len(paths)!=1 {mu.Unlock();t.Fatalf("unpaid job must generate only one system card, calls=%d",len(paths))}
 if paths[0]!="/images/edits" || len(references[0])!=1 || !bytes.Equal(references[0][0],selected){t.Error("system card must use selected direction 2")}
 mu.Unlock()
 var delivered int
 if err:=pool.QueryRow(contextBG(),"SELECT COUNT(*) FROM job_images WHERE job_id=$1 AND kind<>'system_card'",j.ID).Scan(&delivered);err!=nil{t.Fatal(err)}
 if delivered!=0{t.Fatalf("unpaid deliverables=%d",delivered)}
 grantTestItems(t,j.ID,"logo_h")
 if err:=stageBoardsV3(j);err!=nil{t.Fatal(err)}
 mu.Lock();defer mu.Unlock()
 if len(paths)!=2{t.Fatalf("one purchased item adds exactly one request; got %d",len(paths))}
 chosenFound:=false;for _,raw:=range references[1]{if bytes.Equal(raw,selected){chosenFound=true}}
 if !chosenFound || sizeFields[1]!="1024x1024"{t.Fatalf("purchased logo must reuse direction 2 and square size: chosen=%v size=%s",chosenFound,sizeFields[1])}
}

func TestAuditUIJobsPipelineFailureMustNotBecomeDone(t *testing.T) {
	j := commercialJob(t,"running","boards",1)
	commercialWritePNG(t,j.ID,"direction_1.png",commercialPNG(t,90))
	// Deliberately unconfigured: fails immediately, with no network or paid model request.
	t.Setenv("ZEROBRAND_IMG_BASE_URL","")
	t.Setenv("ZEROBRAND_IMG_API_KEY","")
	t.Setenv("ZEROBRAND_IMG_FALLBACK_BASE_URL","")
	t.Setenv("ZEROBRAND_IMG_FALLBACK_API_KEY","")
	err := stageBoardsV3(j)
	var status string
	if scanErr:=pool.QueryRow(contextBG(),"SELECT status FROM jobs WHERE id=$1",j.ID).Scan(&status);scanErr!=nil {t.Fatal(scanErr)}
	t.Logf("all image calls rejected locally: pipeline_err=%v persisted_status=%s",err,status)
	if err==nil {t.Error("failed image generation must propagate an error to worker/refund/retry flow")}
	if status=="done" {t.Error("missing 17 generated images must not become done")}
	story,readErr:=os.ReadFile(filepath.Join(jobDir(j.ID),"brand_story.md"))
	if readErr!=nil && !os.IsNotExist(readErr) {t.Fatal(readErr)}
	if strings.Contains(string(story),"【任务】撰写品牌故事文档") {
		t.Errorf("brand_story.md contains prompt instructions instead of completed brand story: suffix=%q",string(story[len(story)-len("【任务】撰写品牌故事文档"):]))
	}
}

func TestAuditUIJobsCompletedDiskArtifactsMustNotRegenerate(t *testing.T) {
	j := commercialJob(t,"running","boards",2)
 mustExec(t,"UPDATE jobs SET unlocked_packs=ARRAY['all'] WHERE id=$1",j.ID)
	for _, spec:=range itemSpecs(j.Form) {
		if spec.Kind=="brand_story" {continue}
		commercialWritePNG(t,j.ID,spec.Kind+".png",commercialPNG(t,100))
	}
	if err:=os.WriteFile(filepath.Join(jobDir(j.ID),"brand_story.md"),[]byte("# 已完成的品牌故事\n"),0o644);err!=nil {t.Fatal(err)}
	var requests atomic.Int32
	commercialGenerator(t,func(*http.Request){requests.Add(1)})
	if err:=stageBoardsV3(j);err!=nil {t.Fatal(err)}
	if got:=requests.Load();got!=0 {t.Errorf("restart between file save and DB record must reuse valid PNG files; made %d provider requests",got)}
}

func TestAuditUIJobsDownloadUsesGeneratedFilePath(t *testing.T) {
	j := commercialJob(t,"done","done",1)
	raw:=commercialPNG(t,150)
	commercialWritePNG(t,j.ID,"logo_h.png",raw)
	mustExec(t,"UPDATE jobs SET unlocked_packs=ARRAY['all'] WHERE id=$1",j.ID)
	mustExec(t,"INSERT INTO job_images(job_id,kind,idx,file,tokens) VALUES($1,'logo_h',2,$2,0)",j.ID,webPath(j.ID,"logo_h.png"))
	w:=httptest.NewRecorder()
	c,_:=gin.CreateTestContext(w)
	c.Request=httptest.NewRequest("GET","/0brand/api/jobs/"+j.ID+"/download",nil)
	c.Params=gin.Params{{Key:"id",Value:j.ID}}
 vid:="audit-download-"+randHex(6); mustExec(t,"UPDATE jobs SET visitor=$2 WHERE id=$1",j.ID,vid);c.Request.AddCookie(commercialCookie(visitorCookie,vid,0))
	jobDownload(c)
	if w.Code!=200 {t.Fatalf("download status=%d body=%s",w.Code,w.Body.String())}
	z,err:=zip.NewReader(bytes.NewReader(w.Body.Bytes()),int64(w.Body.Len()))
	if err!=nil {t.Fatal(err)}
	t.Logf("file exists at %s; stored path=%s; ZIP entries=%d bytes=%d",filepath.Join(jobDir(j.ID),"logo_h.png"),webPath(j.ID,"logo_h.png"),len(z.File),w.Body.Len())
	if len(z.File)!=1 {t.Errorf("expected 1 existing purchased artifact in ZIP; got %d",len(z.File))}
}

func grantTestItems(t *testing.T,jid string,kinds ...string){
 t.Helper();oid:=auditPaymentOrder(t,jid,"items:test","",90)
 mustExec(t,"UPDATE orders SET status='paid',credited=true,mock=true WHERE id=$1",oid)
 for _,kind:=range kinds{mustExec(t,"INSERT INTO order_items(order_id,item_kind,item_name,price_cents) VALUES($1,$2,$2,90)",oid,kind)}
}
