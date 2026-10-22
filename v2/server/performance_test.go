package app

import (
 "archive/zip"
 "regexp"
 "compress/gzip"
 "testing/fstest"
 "bytes"
 "io"
 "io/fs"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
 "github.com/gin-gonic/gin"
)
func perfAsset(b testing.TB) string {b.Helper();body,e:=webFS.ReadFile("webdist/index.html");if e!=nil{b.Fatal(e)};match:=regexp.MustCompile(`<script[^>]+src="(/assets/[^" ]+\.js)"`).FindSubmatch(body);if len(match)!=2{b.Fatal("current module entry not found")};return string(match[1])}
type discardResponse struct{h http.Header}
func(w *discardResponse) Header()http.Header{return w.h}
func(w *discardResponse)Write(p []byte)(int,error){return len(p),nil}
func(w *discardResponse)WriteHeader(int){}
func BenchmarkPerformanceStatic(b *testing.B){
 r:=buildRouter();path:=basePath+perfAsset(b);req:=httptest.NewRequest("GET",path,nil)
 b.ReportAllocs();b.ResetTimer();for i:=0;i<b.N;i++{r.ServeHTTP(&discardResponse{make(http.Header)},req)}
}
func BenchmarkPerformanceCompressed(b *testing.B){
 dist,_:=fs.Sub(webFS,"webdist");path:=perfAsset(b);req:=httptest.NewRequest("GET",path,nil);req.Header.Set("Accept-Encoding","zstd")
 gin.SetMode(gin.ReleaseMode);r:=gin.New();r.GET(path,func(c *gin.Context){if !servePrecompressedStatic(c,dist,path){b.Fatal("missing precompressed artifact")}})
 b.ReportAllocs();b.ResetTimer();for i:=0;i<b.N;i++{r.ServeHTTP(&discardResponse{make(http.Header)},req)}
}
func BenchmarkPerformanceZIP(b *testing.B){
 // PNG IDAT is already compressed: deterministic incompressible 3MiB payload.
 data:=make([]byte,3<<20);x:=uint32(1);for i:=range data{x^=x<<13;x^=x>>17;x^=x<<5;data[i]=byte(x)}
 b.ReportAllocs();b.SetBytes(int64(len(data)));b.ResetTimer();for i:=0;i<b.N;i++{z:=zip.NewWriter(io.Discard);w,e:=createDownloadEntry(z,"logo.png");if e!=nil{b.Fatal(e)};if _,e=io.Copy(w,bytes.NewReader(data));e!=nil{b.Fatal(e)};if e=z.Close();e!=nil{b.Fatal(e)}}
}
func TestPerformanceStaticAllocationBudget(t *testing.T){r:=buildRouter();req:=httptest.NewRequest("GET",basePath+perfAsset(t),nil);n:=testing.AllocsPerRun(100,func(){r.ServeHTTP(&discardResponse{make(http.Header)},req)});if n>60{t.Fatalf("static hot path allocation budget: %.0f > 60",n)}}
func TestPerformanceCompressedHeadAndRange(t *testing.T){
 dist,_:=fs.Sub(webFS,"webdist");path:=perfAsset(t)
 for _,method:=range []string{"HEAD","GET"}{w:=httptest.NewRecorder();c,_:=gin.CreateTestContext(w);c.Request=httptest.NewRequest(method,path,nil);c.Request.Header.Set("Accept-Encoding","zstd");if method=="GET"{c.Request.Header.Set("Range","bytes=0-9")};if !servePrecompressedStatic(c,dist,path){t.Fatal("missing representation")};if method=="HEAD"&&w.Body.Len()!=0{t.Fatalf("HEAD downloaded %d bytes",w.Body.Len())};if method=="GET"&&(w.Code!=206||w.Body.Len()!=10){t.Fatalf("range: %d %d",w.Code,w.Body.Len())}}
}

func TestPerformanceEncodingNegotiation(t *testing.T){
 var gz bytes.Buffer;z:=gzip.NewWriter(&gz);z.Write([]byte("hello performance"));z.Close()
 dist:=fstest.MapFS{"app.js.gz":&fstest.MapFile{Data:gz.Bytes()},"app.js.zst":&fstest.MapFile{Data:[]byte("zstd fixture")}}
 for _,tc:=range []struct{accept,want string}{{"gzip","gzip"},{"zstd;q=0, gzip;q=1","gzip"},{"ZSTD;Q=0.8, gzip;q=0.2","zstd"},{"zstd;q=0, gzip;q=0",""},{"zstd;q=bogus",""},{"*;q=0.5, zstd;q=0","gzip"},{"x-zstd",""},{"",""}}{
  t.Run(tc.accept,func(t *testing.T){w:=httptest.NewRecorder();c,_:=gin.CreateTestContext(w);c.Request=httptest.NewRequest("GET","/app.js",nil);c.Request.Header.Set("Accept-Encoding",tc.accept);served:=servePrecompressedStatic(c,dist,"/app.js");if served!=(tc.want!="")||w.Header().Get("Content-Encoding")!=tc.want{t.Fatalf("served=%v encoding=%q want %q",served,w.Header().Get("Content-Encoding"),tc.want)};if !strings.Contains(w.Header().Get("Vary"),"Accept-Encoding"){t.Fatal("missing Vary even on identity fallback")};if tc.want=="gzip"{r,e:=gzip.NewReader(w.Body);if e!=nil{t.Fatal(e)};body,e:=io.ReadAll(r);r.Close();if e!=nil||string(body)!="hello performance"{t.Fatal("gzip changed bytes")}}})
 }
}
func TestPerformanceImmutableRevalidation(t *testing.T){
 r:=buildRouter();path:=basePath+perfAsset(t)
 get:=func(enc,tag string)*httptest.ResponseRecorder{w:=httptest.NewRecorder();q:=httptest.NewRequest("GET",path,nil);q.Header.Set("Accept-Encoding",enc);q.Header.Set("If-None-Match",tag);r.ServeHTTP(w,q);return w}
 initial:=get("gzip","");tag:=initial.Header().Get("ETag");if tag==""{t.Fatal("immutable asset has no validator")}
 for _,enc:=range []string{"gzip","zstd","identity"}{w:=get(enc,tag);if w.Code!=304||w.Body.Len()!=0{t.Fatalf("%s revalidation sent body: %d %d",enc,w.Code,w.Body.Len())};if !strings.Contains(w.Header().Get("Vary"),"Accept-Encoding"){t.Fatal("304 missing encoding Vary")}}
 if w:=get("gzip",`W/"different"`);w.Code!=200||w.Body.Len()==0{t.Fatal("changed validator did not return current content")}
 for _,p:=range []string{basePath+"/",basePath+"/assets/absent-abcdefgh.js"}{w:=httptest.NewRecorder();q:=httptest.NewRequest("GET",p,nil);q.Header.Set("If-None-Match",tag);r.ServeHTTP(w,q);if w.Code==304||w.Header().Get("ETag")!=""{t.Fatal("HTML/missing asset reused static validator")}}
}
func BenchmarkPerformanceRevalidation(b *testing.B){r:=buildRouter();path:=perfAsset(b);req:=httptest.NewRequest("GET",basePath+path,nil);req.Header.Set("Accept-Encoding","gzip");req.Header.Set("If-None-Match",`W/"`+strings.TrimPrefix(path,"/assets/")+`"`);b.ReportAllocs();b.ResetTimer();for i:=0;i<b.N;i++{r.ServeHTTP(&discardResponse{make(http.Header)},req)}}
func TestPerformanceZIPStoresOriginalPNG(t *testing.T){
 j:=commercialJob(t,"done","done",0);vid:="perf-zip-"+randHex(5);mustExec(t,"UPDATE jobs SET visitor=$2 WHERE id=$1",j.ID,vid);grantTestItems(t,j.ID,"worldview")
 raw:=commercialPNG(t,40);commercialWritePNG(t,j.ID,"board_1.png",raw);mustExec(t,"INSERT INTO job_images(job_id,kind,idx,file,tokens) VALUES($1,'board',1,$2,0)",j.ID,webPath(j.ID,"board_1.png"))
 w:=commerceRequest(t,"GET","/0brand/api/jobs/"+j.ID+"/download",vid,"");if w.Code!=200{t.Fatalf("download: %d %s",w.Code,w.Body)};z,e:=zip.NewReader(bytes.NewReader(w.Body.Bytes()),int64(w.Body.Len()));if e!=nil||len(z.File)!=1{t.Fatalf("ZIP files: %v",e)};f:=z.File[0];if f.Method!=zip.Store{t.Fatalf("PNG unnecessarily recompressed: method=%d",f.Method)};r,e:=f.Open();if e!=nil{t.Fatal(e)};got,e:=io.ReadAll(r);r.Close();if e!=nil||!bytes.Equal(got,raw){t.Fatal("original PNG bytes changed")};if w.Header().Get("Cache-Control")!="private, no-store"{t.Fatal("download became public")}
}
func BenchmarkPerformanceGzip(b *testing.B){
 path:=perfAsset(b);body,e:=webFS.ReadFile("webdist"+path);if e!=nil{b.Fatal(e)}
 b.Run("Dynamic",func(b *testing.B){b.ReportAllocs();for i:=0;i<b.N;i++{z:=gzip.NewWriter(io.Discard);if _,e:=z.Write(body);e!=nil{b.Fatal(e)};if e:=z.Close();e!=nil{b.Fatal(e)}}})
 b.Run("Precompressed",func(b *testing.B){dist,_:=fs.Sub(webFS,"webdist");req:=httptest.NewRequest("GET",path,nil);req.Header.Set("Accept-Encoding","gzip");r:=gin.New();r.GET(path,func(c *gin.Context){if !servePrecompressedStatic(c,dist,path){b.Fatal("missing gzip")}});b.ReportAllocs();b.ResetTimer();for i:=0;i<b.N;i++{r.ServeHTTP(&discardResponse{make(http.Header)},req)}})
}
func TestPerformanceBenchmarkUsesCurrentEntry(t *testing.T){body,e:=webFS.ReadFile("webdist/index.html");if e!=nil{t.Fatal(e)};if !strings.Contains(string(body),`src="`+perfAsset(t)+`"`){t.Fatalf("benchmark chose historical entry: %s",perfAsset(t))}}
