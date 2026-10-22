package app

import (
 "archive/zip"
 "bytes"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "sync/atomic"
 "testing"
)

// One continuous transaction journey. Only loopback zerobrand_test and a local
// fake image server are used. No worker or real payment client is started.
func TestPerformanceReleaseCompleteIsolatedJourney(t *testing.T) {
 commercialRequireTestDB(t)
 t.Setenv("ZEROBRAND_GEN_DIR",t.TempDir())
 t.Setenv("ZEROBRAND_DIRECTIONS_PAYWALL","1")
 auditPaymentConfig(t,true)
 var calls atomic.Int32
 commercialGenerator(t,func(r *http.Request){if !strings.HasPrefix(r.Host,"127.0.0.1:"){t.Error("nonlocal generation")};calls.Add(1)})
 var jid,vid string
 t.Cleanup(func(){
  var jobs,orders,visitors int
  if e:=pool.QueryRow(contextBG(),"SELECT count(*) FROM jobs WHERE id=$1",jid).Scan(&jobs);e!=nil{t.Error(e)}
  if e:=pool.QueryRow(contextBG(),"SELECT count(*) FROM orders WHERE job_id=$1",jid).Scan(&orders);e!=nil{t.Error(e)}
  if e:=pool.QueryRow(contextBG(),"SELECT count(*) FROM visitors WHERE id=$1",vid).Scan(&visitors);e!=nil{t.Error(e)}
  if jobs+orders+visitors!=0{t.Errorf("fixture residue jobs=%d orders=%d visitors=%d",jobs,orders,visitors)}
  t.Logf("cleanup verified: jobs=%d orders=%d visitors=%d",jobs,orders,visitors)
 })
 vid=reviewFlowVisitor(t)
 key:="performance-journey-"+randHex(12)
 jid=reviewFlowCreateID(t,reviewFlowCreate(t,vid,key,"隔离性能验收"))
 t.Cleanup(func(){
  mustExec(t,"DELETE FROM agent_commissions WHERE order_id IN (SELECT id FROM orders WHERE job_id=$1)",jid)
  mustExec(t,"DELETE FROM order_items WHERE order_id IN (SELECT id FROM orders WHERE job_id=$1)",jid)
  mustExec(t,"DELETE FROM invite_codes WHERE order_id IN (SELECT id FROM orders WHERE job_id=$1)",jid)
  mustExec(t,"DELETE FROM audit_events WHERE object_id=$1 OR object_id IN (SELECT id FROM orders WHERE job_id=$1)",jid)
  mustExec(t,"DELETE FROM orders WHERE job_id=$1",jid)
  mustExec(t,"DELETE FROM usage_events WHERE job_id=$1",jid)
  mustExec(t,"DELETE FROM job_images WHERE job_id=$1",jid)
 })
 if replay:=reviewFlowCreateID(t,reviewFlowCreate(t,vid,key,"隔离性能验收"));replay!=jid{t.Fatal("creation replay duplicated job")}
 load:=func()jobRow{var j jobRow;var form,snapshot []byte;e:=pool.QueryRow(contextBG(),"SELECT id,status,stage,form,chosen,delivery_version,COALESCE(direction_snapshot,'null'::jsonb) FROM jobs WHERE id=$1",jid).Scan(&j.ID,&j.Status,&j.Stage,&form,&j.Chosen,&j.DeliveryVersion,&snapshot);if e!=nil{t.Fatal(e)};if e=json.Unmarshal(form,&j.Form);e!=nil{t.Fatal(e)};j.DirectionSnapshot=decodeDirectionSnapshot(snapshot);return j}
 request:=func(method,path,body string)*httptest.ResponseRecorder{return commerceRequest(t,method,path,vid,body)}
 if j:=load();j.Status!="waiting_unlock"||j.Stage!="unlock"{t.Fatalf("initial state %s/%s",j.Status,j.Stage)}
 pay:=func(path,body string)string{
  w:=request("POST",path,body);if w.Code!=201&&w.Code!=200{t.Fatalf("checkout %d: %s",w.Code,w.Body)}
  var order struct{ID string `json:"id"`;Amount int64 `json:"amount_cents"`};if e:=json.Unmarshal(w.Body.Bytes(),&order);e!=nil||order.ID==""{t.Fatalf("checkout response %s",w.Body)}
  replay:=request("POST",path,body);var again struct{ID string `json:"id"`};json.Unmarshal(replay.Body.Bytes(),&again);if again.ID!=order.ID{t.Fatalf("checkout duplicate %d %s",replay.Code,replay.Body)}
  var mock bool;var amount int64;if e:=pool.QueryRow(contextBG(),"SELECT mock,amount_cents FROM orders WHERE id=$1",order.ID).Scan(&mock,&amount);e!=nil||!mock||amount!=order.Amount{t.Fatal("order is not isolated mock or amount mismatch")}
  // Age only this test order, letting the local mock query return SUCCESS.
  mustExec(t,"UPDATE orders SET created_at=now()-interval '3 seconds' WHERE id=$1",order.ID)
  w=request("POST","/0brand/api/pay/orders/"+order.ID+"/confirm",`{}`);if w.Code!=200{t.Fatalf("confirm %d: %s",w.Code,w.Body)}
  var status string;var credited bool;if e:=pool.QueryRow(contextBG(),"SELECT status,credited FROM orders WHERE id=$1",order.ID).Scan(&status,&credited);e!=nil||status!="paid"||!credited{t.Fatalf("settlement %s/%v err=%v",status,credited,e)}
  if e:=settleOrder(order.ID,"audit-merchant","audit-app",amount,"MOCKTX"+order.ID);e!=nil{t.Fatalf("duplicate settlement %v",e)}
  t.Logf("mock checkout/settlement verified: %d cents; repeated checkout reused order",amount)
  return order.ID
 }
 unlock:=pay("/0brand/api/jobs/"+jid+"/unlock",`{}`)
 var amount int64;pool.QueryRow(contextBG(),"SELECT amount_cents FROM orders WHERE id=$1",unlock).Scan(&amount);if amount!=990{t.Fatalf("unlock amount %d",amount)}
 if j:=load();j.Status!="queued"||j.Stage!="directions"{t.Fatal("unlock did not schedule directions")}
 if e:=stageDirections(load());e!=nil{t.Fatal(e)}
 if j:=load();j.Status!="ready_to_choose"{t.Fatalf("directions: %s",j.Status)}
 for i:=0;i<3;i++{w:=request("GET",fmt.Sprintf("/0brand/gen/%s/direction_%d.png",jid,i),"");if w.Code!=200{t.Fatalf("direction preview %d",w.Code)}}
 w:=request("POST","/0brand/api/jobs/"+jid+"/choose",`{"direction":1}`);if w.Code!=200{t.Fatalf("choose %d %s",w.Code,w.Body)}
 if e:=stageLogo(load());e!=nil{t.Fatal(e)}
 if j:=load();j.Status!="ready_to_confirm"{t.Fatalf("formal logo state %s",j.Status)}
 w=request("POST","/0brand/api/jobs/"+jid+"/logo/confirm",`{}`);if w.Code!=200{t.Fatalf("logo confirm %d %s",w.Code,w.Body)}
 if e:=stageBoardsV3(load());e!=nil{t.Fatal(e)}
 w=request("GET","/0brand/api/jobs/"+jid+"/download","");if w.Code!=409{t.Fatalf("unbought applications downloadable: %d",w.Code)}
 pay("/0brand/api/pay/create",fmt.Sprintf(`{"job_id":%q,"items":["logo_h","brand_story"]}`,jid))
 if e:=stageBoardsV3(load());e!=nil{t.Fatal(e)}
 if j:=load();j.Status!="done"||j.Stage!="done"{t.Fatalf("delivery state %s/%s",j.Status,j.Stage)}
 count:=calls.Load();if e:=stageBoardsV3(load());e!=nil{t.Fatal(e)};if calls.Load()!=count{t.Fatal("completed replay generated/charged again")}
 w=request("GET","/0brand/api/jobs/"+jid+"/download","");if w.Code!=200||w.Header().Get("Cache-Control")!="private, no-store"{t.Fatalf("download %d %s",w.Code,w.Body)}
 z,e:=zip.NewReader(bytes.NewReader(w.Body.Bytes()),int64(w.Body.Len()));if e!=nil{t.Fatal(e)};if len(z.File)!=2{t.Fatalf("archive included wrong rights: %d files",len(z.File))}
 for _,f:=range z.File{if f.Name!="logo_h.png"&&f.Name!="brand_story.md"{t.Fatalf("unbought archive entry %s",f.Name)};r,e:=f.Open();if e!=nil{t.Fatal(e)};data,e:=io.ReadAll(r);r.Close();if e!=nil{t.Fatal(e)};original,e:=os.ReadFile(filepath.Join(jobDir(jid),f.Name));if e!=nil||!bytes.Equal(data,original){t.Fatalf("archive changed original %s",f.Name)};if f.Name=="logo_h.png"&&f.Method!=zip.Store{t.Fatal("PNG performance change absent")}}
 if w=commerceRequest(t,"GET","/0brand/api/jobs/"+jid+"/download","foreign-performance-visitor","");w.Code!=404{t.Fatal("foreign download allowed")}
 var total,real int;pool.QueryRow(contextBG(),"SELECT count(*),count(*) FILTER (WHERE NOT mock) FROM orders WHERE job_id=$1",jid).Scan(&total,&real);if total!=2||real!=0{t.Fatalf("unexpected billing rows: total=%d real=%d",total,real)}
 t.Logf("complete journey passed: 3 directions + formal Logo + paid applications; mock image calls=%d; orders=%d all mock; archive=%d original files",count,total,len(z.File))
}
