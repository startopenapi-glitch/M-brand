package app

import (
 "archive/zip"
 "bytes"
 "fmt"
 "io"
 "net/http"
 "net/http/httptest"
 "encoding/json"
 "strings"
 "testing"
 "time"
 "github.com/gin-gonic/gin"
)

func TestReviewEveryBrandFreeAfterPreviousUse(t *testing.T){
 resetCreateLimiter();t.Cleanup(resetCreateLimiter)
 t.Setenv("ZEROBRAND_TRIAL_PER_SESSION","1");t.Setenv("ZEROBRAND_TRIAL_PER_IP","2");t.Setenv("ZEROBRAND_TRIAL_DAILY_CAP","20")
 _,vid:=auditPaymentJob(t);mustExec(t,"UPDATE visitors SET trial_used=999 WHERE id=$1",vid)
 t.Cleanup(func(){mustExec(t,"DELETE FROM audit_events WHERE actor=$1","visitor:"+vid);mustExec(t,"DELETE FROM jobs WHERE visitor=$1",vid)})
 for _,name:=range []string{"新品牌一","新品牌二","新品牌三"}{r:=commerceRequest(t,"POST","/0brand/api/jobs",vid,`{"cn_name":"`+name+`","industry":"测试"}`);if r.Code!=201{t.Fatalf("new brand must remain free: %d %s",r.Code,r.Body)}}
}

func TestReviewAddonFailureKeepsFulfilledTicket(t *testing.T){
 j:=commercialJob(t,"running","boards",0);code:=upperCode("RVFUL");vid:="review-owner-"+randHex(5)
 mustExec(t,"INSERT INTO invite_codes(code,max_uses,used,bound_visitor) VALUES($1,1,1,$2)",code,vid)
 t.Cleanup(func(){mustExec(t,"DELETE FROM invite_codes WHERE code=$1",code)})
 mustExec(t,"UPDATE jobs SET quota_code=$2,visitor=$3 WHERE id=$1",j.ID,code,vid)
 commercialWritePNG(t,j.ID,"system_card.png",commercialPNG(t,10));commercialWritePNG(t,j.ID,"direction_0.png",commercialPNG(t,20))
 for i:=1;i<=4;i++{f:=fmt.Sprintf("board_%d.png",i);commercialWritePNG(t,j.ID,f,commercialPNG(t,byte(i*30)));mustExec(t,"INSERT INTO job_images(job_id,kind,idx,file,tokens) VALUES($1,'board',$2,$3,0)",j.ID,i,webPath(j.ID,f))}
 grantTestItems(t,j.ID,"logo_h")
 t.Setenv("ZEROBRAND_IMG_BASE_URL","");t.Setenv("ZEROBRAND_IMG_API_KEY","");t.Setenv("ZEROBRAND_IMG_FALLBACK_BASE_URL","");t.Setenv("ZEROBRAND_IMG_FALLBACK_API_KEY","")
 err:=stageBoardsV3(j);if err==nil{t.Fatal("expected isolated missing-provider failure")};markJobFailed(j.ID,err.Error())
 rights,e:=jobEntitlements(contextBG(),pool,j.ID);if e!=nil||!rights["worldview"]||!rights["logo_h"]{t.Fatalf("prior rights revoked %v %v",rights,e)}
 var used int;var refunded bool;pool.QueryRow(contextBG(),"SELECT used FROM invite_codes WHERE code=$1",code).Scan(&used);pool.QueryRow(contextBG(),"SELECT refunded FROM jobs WHERE id=$1",j.ID).Scan(&refunded)
 if used!=1||refunded{t.Fatalf("fulfilled ticket refunded: used=%d refunded=%v",used,refunded)}
 if _,e:=retryFailedJob(contextBG(),j.ID,vid,"",false);e!=nil{t.Fatal(e)}
}

func TestReviewNativeResumeUsesIdenticalRequest(t *testing.T){
 key:=auditPaymentConfig(t,false);resetCreateLimiter();jid,vid:=auditPaymentJob(t)
 var bodies [][]byte;var oid string
 wxHTTP=&http.Client{Transport:auditPaymentTransport(func(req *http.Request)(*http.Response,error){
  body:="";if req.Method=="POST"{b,_:=io.ReadAll(req.Body);bodies=append(bodies,b);var p map[string]any;json.Unmarshal(b,&p);oid=p["out_trade_no"].(string);if len(bodies)==1{return nil,fmt.Errorf("response lost after channel accepted")};body=`{"code_url":"weixin://recovered"}`
  }else{body=fmt.Sprintf(`{"out_trade_no":%q,"trade_state":"NOTPAY","mchid":"audit-merchant","appid":"audit-app","amount":{"total":1900,"currency":"CNY"}}`,oid)}
  ts:=fmt.Sprint(time.Now().Unix());h:=http.Header{};h.Set("Wechatpay-Serial","audit-platform-serial");h.Set("Wechatpay-Timestamp",ts);h.Set("Wechatpay-Nonce","review");h.Set("Wechatpay-Signature",signWithPlatform(key,ts+"\nreview\n"+body+"\n"));return &http.Response{StatusCode:200,Header:h,Body:io.NopCloser(strings.NewReader(body))},nil
 })}
 r:=commerceRequest(t,"POST","/0brand/api/pay/create",vid,`{"job_id":"`+jid+`","items":["logo_h"]}`);if r.Code!=202||oid==""{t.Fatalf("unknown %d %s",r.Code,r.Body)}
 t.Cleanup(func(){mustExec(t,"DELETE FROM order_items WHERE order_id=$1",oid);mustExec(t,"DELETE FROM orders WHERE id=$1",oid)})
 mustExec(t,"UPDATE orders SET checkout_lease_until=now()-interval '1 second' WHERE id=$1",oid)
 r=commerceRequest(t,"POST","/0brand/api/pay/orders/"+oid+"/resume",vid,`{}`)
 if r.Code!=200||len(bodies)!=2||!bytes.Equal(bodies[0],bodies[1]){t.Fatalf("resume changed financial request: status=%d requests=%q",r.Code,bodies)}
}

func TestReviewNewDeliverablesCanPublishAsCase(t *testing.T){
 j:=commercialJob(t,"done","done",0);grantTestItems(t,j.ID,"worldview","logo_logic","color_typo","app_overview")
 for i:=1;i<=4;i++{kind:=legacyBoardKind(i);commercialWritePNG(t,j.ID,kind+".png",commercialPNG(t,byte(i*30)));mustExec(t,"INSERT INTO job_images(job_id,kind,idx,file,tokens) VALUES($1,$2,$3,$4,0)",j.ID,kind,i,webPath(j.ID,kind+".png"))}
 t.Cleanup(func(){mustExec(t,"DELETE FROM cases WHERE job_id=$1",j.ID);mustExec(t,"DELETE FROM audit_events WHERE object_id=$1",j.ID)})
 r:=gin.New();r.POST("/cases",adminPublishCase);w:=httptest.NewRecorder();req:=httptest.NewRequest("POST","/cases",strings.NewReader(`{"job_id":"`+j.ID+`","title":"隔离案例"}`));req.Header.Set("Content-Type","application/json");r.ServeHTTP(w,req)
 if w.Code!=201{t.Fatalf("new paid outputs cannot publish: %d %s",w.Code,w.Body)}
}

func TestReviewFailedRunDoesNotStrandNewPurchase(t *testing.T){
 auditPaymentConfig(t,false);jid,_:=auditPaymentJob(t)
 mustExec(t,"UPDATE jobs SET status='running',stage='boards',delivery_version=1 WHERE id=$1",jid)
 oid:=auditPaymentOrder(t,jid,"items:logo_h","",390);mustExec(t,"INSERT INTO order_items(order_id,item_kind,item_name,price_cents) VALUES($1,'logo_h','logo',390)",oid)
 if e:=settleOrder(oid,"audit-merchant","audit-app",390,"TX"+oid);e!=nil{t.Fatal(e)}
 markJobFailed(jid,"previous request failed",1)
 var state string;var version int64;pool.QueryRow(contextBG(),"SELECT status,delivery_version FROM jobs WHERE id=$1",jid).Scan(&state,&version)
 if state!="queued"||version!=2{t.Fatalf("paid queue stranded %s %d",state,version)}
 // With no further purchase, one failed retry stays error (no hot retry loop).
 markJobFailed(jid,"request still failed",2);pool.QueryRow(contextBG(),"SELECT status FROM jobs WHERE id=$1",jid).Scan(&state);if state!="error"{t.Fatalf("unbounded retry %s",state)}
}

func TestReviewNewPurchaseQueueKeepsUnfulfilledTicket(t *testing.T){
 jid,vid:=auditPaymentJob(t);code:=upperCode("RVKEEP");mustExec(t,"INSERT INTO invite_codes(code,max_uses,used,bound_visitor) VALUES($1,1,1,$2)",code,vid);t.Cleanup(func(){mustExec(t,"DELETE FROM invite_codes WHERE code=$1",code)})
 mustExec(t,"UPDATE jobs SET quota_code=$2,delivery_version=2,status='running',stage='boards' WHERE id=$1",jid,code)
 markJobFailed(jid,"old request failed",1)
 var state string;var refunded bool;pool.QueryRow(contextBG(),"SELECT status,refunded FROM jobs WHERE id=$1",jid).Scan(&state,&refunded)
 if state!="queued"||refunded{t.Fatalf("queued retry lost entitlement: %s %v",state,refunded)}
}

func TestReviewSinglePaidBoardHasExactFileRights(t *testing.T){
 j:=commercialJob(t,"done","done",0);vid:="review-board-"+randHex(5);mustExec(t,"UPDATE jobs SET visitor=$2,quota_code='historic-refunded',refunded=true WHERE id=$1",j.ID,vid)
 for i:=1;i<=4;i++{f:=fmt.Sprintf("board_%d.png",i);commercialWritePNG(t,j.ID,f,commercialPNG(t,byte(i*30)));mustExec(t,"INSERT INTO job_images(job_id,kind,idx,file,tokens) VALUES($1,'board',$2,$3,0)",j.ID,i,webPath(j.ID,f))}
 grantTestItems(t,j.ID,"worldview")
 for i:=1;i<=4;i++{r:=commerceRequest(t,"GET",fmt.Sprintf("/0brand/gen/%s/board_%d.png",j.ID,i),vid,"");want:=404;if i==1{want=200};if r.Code!=want{t.Fatalf("board %d HTTP %d expected %d",i,r.Code,want)}}
 r:=commerceRequest(t,"GET","/0brand/api/jobs/"+j.ID+"/download",vid,"");if r.Code!=200{t.Fatalf("zip %d %s",r.Code,r.Body)}
 z,e:=zip.NewReader(bytes.NewReader(r.Body.Bytes()),int64(r.Body.Len()));if e!=nil{t.Fatal(e)};if len(z.File)!=1||z.File[0].Name!="board_1.png"{t.Fatalf("wrong ZIP contents %v",z.File)}
}

func TestReviewRecordedBoardPackKeepsAllFour(t *testing.T){
 j:=commercialJob(t,"done","done",0);vid:="review-pack-"+randHex(5);mustExec(t,"UPDATE jobs SET visitor=$2 WHERE id=$1",j.ID,vid)
 oid:=auditPaymentOrder(t,j.ID,"boards","",1990);mustExec(t,"UPDATE orders SET status='paid',credited=true WHERE id=$1",oid)
 for i:=1;i<=4;i++{f:=fmt.Sprintf("board_%d.png",i);commercialWritePNG(t,j.ID,f,commercialPNG(t,byte(i*30)));mustExec(t,"INSERT INTO job_images(job_id,kind,idx,file,tokens) VALUES($1,'board',$2,$3,0)",j.ID,i,webPath(j.ID,f));r:=commerceRequest(t,"GET","/0brand/"+webPath(j.ID,f),vid,"");if r.Code!=200{t.Fatalf("old purchased board %d locked: %d",i,r.Code)}}
 r:=commerceRequest(t,"GET","/0brand/api/jobs/"+j.ID+"/detail",vid,"");var d struct{Items []struct{Kind string `json:"kind"`;Purchased bool `json:"purchased"`}};json.Unmarshal(r.Body.Bytes(),&d);for _,it:=range d.Items{if it.Kind=="color_typo"&&!it.Purchased{t.Fatal("old four-board third item locked")}}
 r=commerceRequest(t,"GET","/0brand/api/jobs/"+j.ID+"/download",vid,"");z,e:=zip.NewReader(bytes.NewReader(r.Body.Bytes()),int64(r.Body.Len()));if e!=nil||len(z.File)!=4{t.Fatalf("old board ZIP lost contents: %v",e)}
}
