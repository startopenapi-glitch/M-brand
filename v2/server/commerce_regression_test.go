package app

import (
 "bytes"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "net/http/httptest"
 "strings"
 "sync"
 "testing"
 "time"
 "os"
 "path/filepath"
)

func commerceRequest(t *testing.T,method,path,vid,body string)*httptest.ResponseRecorder{
 t.Helper();req:=httptest.NewRequest(method,path,bytes.NewBufferString(body));req.Header.Set("Content-Type","application/json")
 req.AddCookie(commercialCookie(visitorCookie,vid,0));rec:=httptest.NewRecorder();buildRouter().ServeHTTP(rec,req);return rec
}
func TestCommerceConcurrentCheckoutSingleOrder(t *testing.T){
 auditPaymentConfig(t,true);resetCreateLimiter();jid,vid:=auditPaymentJob(t)
 body:=`{"job_id":"`+jid+`","items":["logo_h"]}`;var wg sync.WaitGroup;out:=make(chan *httptest.ResponseRecorder,6)
 for i:=0;i<6;i++{wg.Add(1);go func(){defer wg.Done();out<-commerceRequest(t,"POST","/0brand/api/pay/create",vid,body)}()};wg.Wait();close(out)
 ids:=map[string]bool{};for r:=range out{if r.Code!=200&&r.Code!=201{t.Fatalf("checkout %d %s",r.Code,r.Body)};var p map[string]any;json.Unmarshal(r.Body.Bytes(),&p);ids[p["id"].(string)]=true}
 for id:=range ids{t.Cleanup(func(){mustExec(t,"DELETE FROM order_items WHERE order_id=$1",id);mustExec(t,"DELETE FROM orders WHERE id=$1",id)})};if len(ids)!=1{t.Fatalf("duplicate orders: %v",ids)}
}
func TestCommercePayoutRetryOneLedgerEntry(t *testing.T){
 aid,_:=auditPaymentAgent(t,1000);oid:=auditPaymentOrder(t,"","","",2500)
 mustExec(t,`INSERT INTO agent_commissions(agent_id,order_id,order_amount_cents,commission_cents) VALUES($1,$2,2500,1000)`,aid,oid)
 r:=auditPaymentPayoutRouter();body:=[]byte(`{"AmountCents":100,"RequestKey":"stable-payout-request-1"}`)
 for i:=0;i<3;i++{p:=auditPaymentPOST(r,"/agents/"+aid+"/payout",body);if p.Code!=200{t.Fatalf("%d %s",p.Code,p.Body)}}
 var n int;var paid,settled int64
 pool.QueryRow(contextBG(),"SELECT COUNT(*),SUM(amount_cents) FROM agent_payouts WHERE agent_id=$1",aid).Scan(&n,&paid)
 pool.QueryRow(contextBG(),"SELECT settled_cents FROM agent_commissions WHERE order_id=$1",oid).Scan(&settled)
 if n!=1||paid!=100||settled!=100{t.Fatalf("duplicate payout n=%d paid=%d settled=%d",n,paid,settled)}
 if p:=auditPaymentPOST(r,"/agents/"+aid+"/payout",[]byte(`{"AmountCents":200,"RequestKey":"stable-payout-request-1"}`));p.Code!=409{t.Fatal("same key changed amount accepted")}
}
func TestCommerceConcurrentSettlementExactOnce(t *testing.T){
 auditPaymentConfig(t,false);jid,_:=auditPaymentJob(t);aid,code:=auditPaymentAgent(t,0);oid:=auditPaymentOrder(t,jid,"items:logo_h",code,390)
 mustExec(t,"INSERT INTO order_items(order_id,item_kind,item_name,price_cents) VALUES($1,'logo_h','logo',390)",oid)
 var wg sync.WaitGroup;errs:=make(chan error,8);for i:=0;i<8;i++{wg.Add(1);go func(){defer wg.Done();errs<-settleOrder(oid,"audit-merchant","audit-app",390,"TX"+oid)}()};wg.Wait();close(errs);for e:=range errs{if e!=nil{t.Fatal(e)}}
 var n int;var earned,version int64
 pool.QueryRow(contextBG(),"SELECT COUNT(*) FROM agent_commissions WHERE order_id=$1",oid).Scan(&n)
 pool.QueryRow(contextBG(),"SELECT total_earned_cents FROM agents WHERE id=$1",aid).Scan(&earned)
 pool.QueryRow(contextBG(),"SELECT delivery_version FROM jobs WHERE id=$1",jid).Scan(&version)
 if n!=1||earned!=156||version!=1{t.Fatalf("duplicate settlement %d %d %d",n,earned,version)}
}
func TestCommerceOldPaidPackAndFourBoardsVisible(t *testing.T){
 jid,vid:=auditPaymentJob(t);oid:=auditPaymentOrder(t,jid,"logo","",990);mustExec(t,"UPDATE orders SET status='paid',credited=true WHERE id=$1",oid)
 rights,e:=jobEntitlements(contextBG(),pool,jid);if e!=nil||!rights["logo_h"]||rights["card_front"]{t.Fatalf("historic pack %v %v",rights,e)}
 mustExec(t,"UPDATE jobs SET quota_code='recorded-old-ticket',refunded=false WHERE id=$1",jid)
 dir:=t.TempDir();t.Setenv("ZEROBRAND_GEN_DIR",dir);os.MkdirAll(filepath.Join(dir,jid),0755)
 for i:=1;i<=4;i++{file:=fmt.Sprintf("gen/%s/board_%d.png",jid,i);mkTestPNG(t,filepath.Join(dir,jid,fmt.Sprintf("board_%d.png",i)));mustExec(t,"INSERT INTO job_images(job_id,kind,idx,file,tokens) VALUES($1,'board',$2,$3,0)",jid,i,file)}
 rec:=commerceRequest(t,"GET","/0brand/api/jobs/"+jid+"/detail",vid,"");if rec.Code!=200{t.Fatalf("detail %d %s",rec.Code,rec.Body)}
 var d struct{Items []struct{Kind string `json:"kind"`; File string `json:"file"`; Purchased bool `json:"purchased"`}}
 json.Unmarshal(rec.Body.Bytes(),&d);n:=0;for _,it:=range d.Items{if strings.Contains(it.File,"board_"){n++};if it.Kind=="card_front"&&it.Purchased{t.Fatal("old ticket expanded to 17 rights")}}
 if n!=4{t.Fatalf("historic files hidden: %s",rec.Body)}
}
func TestCommerceExpiredSignedClosedBecomesTerminal(t *testing.T){
 key:=auditPaymentConfig(t,false);oid:=auditPaymentOrder(t,"","","",390);mustExec(t,"UPDATE orders SET status='expired' WHERE id=$1",oid)
 body:=fmt.Sprintf(`{"out_trade_no":%q,"trade_state":"CLOSED","mchid":"audit-merchant","appid":"audit-app","amount":{"total":390,"currency":"CNY"}}`,oid)
 wxHTTP=&http.Client{Transport:auditPaymentTransport(func(req *http.Request)(*http.Response,error){ts:=fmt.Sprint(time.Now().Unix());h:=http.Header{};h.Set("Wechatpay-Serial","audit-platform-serial");h.Set("Wechatpay-Timestamp",ts);h.Set("Wechatpay-Nonce","nonce");h.Set("Wechatpay-Signature",signWithPlatform(key,ts+"\nnonce\n"+body+"\n"));return &http.Response{StatusCode:200,Header:h,Body:io.NopCloser(strings.NewReader(body))},nil})}
 queryAndSettle(oid);if s:=orderStatus(t,oid);s!="closed"{t.Fatalf("expired stuck: %s",s)}
}
func TestCommerceMockIdentitiesCorrect(t *testing.T){
 auditPaymentConfig(t,true);jid,_:=auditPaymentJob(t);oid:=auditPaymentOrder(t,jid,"items:logo_h","",390)
 mustExec(t,"INSERT INTO order_items(order_id,item_kind,item_name,price_cents) VALUES($1,'logo_h','logo',390)",oid)
 mustExec(t,"UPDATE orders SET mock=true,created_at=now()-interval '5 seconds' WHERE id=$1",oid);queryAndSettle(oid)
 if orderStatus(t,oid)!="paid"{t.Fatal("configured mock identities prevent settlement")}
}

func TestCommerceCheckoutLeaseIndependentOfPollStamp(t *testing.T){
 auditPaymentConfig(t,true);jid,vid:=auditPaymentJob(t);oid:=auditPaymentOrder(t,jid,"items:logo_h","",390)
 mustExec(t,"UPDATE orders SET mock=true,updated_at=now(),checkout_lease_until=now()-interval '1 second' WHERE id=$1",oid)
 rec:=commerceRequest(t,"POST","/0brand/api/pay/orders/"+oid+"/resume",vid,`{}`)
 if rec.Code!=200||!strings.Contains(rec.Body.String(),"wxp://mock/"){t.Fatalf("poll stamp blocked recovery %d %s",rec.Code,rec.Body)}
}
func TestCommerceMixedCaseReferralCaptured(t *testing.T){
 auditPaymentConfig(t,true);resetCreateLimiter();jid,vid:=auditPaymentJob(t);aid,_:=auditPaymentAgent(t,0)
 mustExec(t,"UPDATE agents SET code='AGaBcD1234' WHERE id=$1",aid)
 req:=httptest.NewRequest("POST","/0brand/api/pay/create",strings.NewReader(`{"job_id":"`+jid+`","items":["logo_h"]}`));req.Header.Set("Content-Type","application/json");req.AddCookie(commercialCookie(visitorCookie,vid,0));req.AddCookie(&http.Cookie{Name:refCookie,Value:"AGABCD1234"});rec:=httptest.NewRecorder();buildRouter().ServeHTTP(rec,req)
 if rec.Code!=201{t.Fatalf("%d %s",rec.Code,rec.Body)}
 var response struct{ID string `json:"id"`};json.Unmarshal(rec.Body.Bytes(),&response)
 t.Cleanup(func(){mustExec(t,"DELETE FROM order_items WHERE order_id=$1",response.ID);mustExec(t,"DELETE FROM orders WHERE id=$1",response.ID)})
 var got string;var pct int;pool.QueryRow(contextBG(),"SELECT agent_id,agent_pct FROM orders WHERE id=$1",response.ID).Scan(&got,&pct);if got!=aid||pct!=40{t.Fatalf("referral lost %s %d",got,pct)}
}
func TestCommerceSettlementRollbackIsAtomic(t *testing.T){
 auditPaymentConfig(t,false);jid,_:=auditPaymentJob(t);aid,code:=auditPaymentAgent(t,0);oid:=auditPaymentOrder(t,jid,"items:logo_h",code,390)
 mustExec(t,"INSERT INTO order_items(order_id,item_kind,item_name,price_cents) VALUES($1,'logo_h','logo',390)",oid)
 constraint:="audit_reject_"+randHex(4)
 // This isolated constraint rejects only this fixture order's commission.
 mustExec(t,"ALTER TABLE agent_commissions ADD CONSTRAINT "+constraint+" CHECK(order_id <> '"+oid+"')")
 t.Cleanup(func(){mustExec(t,"ALTER TABLE agent_commissions DROP CONSTRAINT IF EXISTS "+constraint)})
 if e:=settleOrder(oid,"audit-merchant","audit-app",390,"TX"+oid);e==nil{t.Fatal("injected ledger failure was ignored")}
 var credited bool;var status string;var version,earned int64
 pool.QueryRow(contextBG(),"SELECT status,credited FROM orders WHERE id=$1",oid).Scan(&status,&credited)
 pool.QueryRow(contextBG(),"SELECT delivery_version FROM jobs WHERE id=$1",jid).Scan(&version)
 pool.QueryRow(contextBG(),"SELECT total_earned_cents FROM agents WHERE id=$1",aid).Scan(&earned)
 if status!="pending"||credited||version!=0||earned!=0{t.Fatalf("partial commit: %s %v %d %d",status,credited,version,earned)}
}

func TestCommerceStaticCacheOnlyExistingHashedAssets(t *testing.T){
 files,e:=webFS.ReadDir("webdist/assets");if e!=nil{t.Fatal(e)};asset:="";for _,f:=range files{if strings.HasSuffix(f.Name(),".js")&&strings.Contains(f.Name(),"-"){asset=f.Name();break}}
 if asset==""{t.Fatal("no bundled hashed asset")}
 for _,tc:=range []struct{path string;status int;immutable bool}{{"/0brand/assets/absent-audit.js",404,false},{"/0brand/assets/"+asset,200,true}}{
  rec:=httptest.NewRecorder();buildRouter().ServeHTTP(rec,httptest.NewRequest("GET",tc.path,nil));if rec.Code!=tc.status{t.Fatalf("%s %d",tc.path,rec.Code)}
  h:=rec.Header().Get("Cache-Control");if strings.Contains(h,"immutable")!=tc.immutable{t.Fatalf("bad cache %s %s",tc.path,h)}
 }
}
