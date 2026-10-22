package app
import ("archive/zip";"fmt";"io";"net/http";"os";"path/filepath";"strings";"github.com/gin-gonic/gin")
// Compatibility URL only; no legacy purchase flow remains.
func packPayCreate(c *gin.Context){alacartePayCreate(c)}
func jobDownload(c *gin.Context) {
 jid:=c.Param("id");if !requireJobOwner(c,jid){return}
 rights,err:=jobEntitlements(c,pool,jid);if err!=nil{c.JSON(503,gin.H{"error":"暂时无法读取权益"});return}
 rows,err:=pool.Query(c,"SELECT kind,idx,COALESCE(file,'') FROM job_images WHERE job_id=$1 ORDER BY id",jid)
 if err!=nil{c.JSON(503,gin.H{"error":"暂时无法读取交付物"});return}
 type entry struct{name string; file *os.File};var files []entry
 defer func(){for _,f:=range files{f.file.Close()}}()
 for rows.Next(){var kind,path string;var idx int;if err=rows.Scan(&kind,&idx,&path);err!=nil{break}
  if !rights[artifactKind(kind,idx)] || kind=="direction" || kind=="system_card" || kind=="system" || path=="" {continue}
  // Always deliver the original PNG under its actual extension.
  if strings.HasSuffix(path,".webp"){path=strings.TrimSuffix(path,".webp")+".png"}
  var f *os.File;f,err=openArtifact(jid,path);if err!=nil{break};files=append(files,entry{filepath.Base(path),f})
 }
 re:=rows.Err();rows.Close()
 if err!=nil || re!=nil{c.JSON(409,gin.H{"error":"部分交付物暂不可用，请稍后重试或联系支持"});return}
 if len(files)==0{c.JSON(409,gin.H{"error":"已购内容尚未生成完成"});return}
 // Build before sending headers so disk/zip errors never masquerade as an empty successful ZIP.
 tmp,err:=os.CreateTemp("","0brand-download-*.zip");if err!=nil{c.JSON(503,gin.H{"error":"暂时无法打包"});return};defer os.Remove(tmp.Name());defer tmp.Close()
 z:=zip.NewWriter(tmp)
 for _,f:=range files{var dst io.Writer;dst,err=createDownloadEntry(z,f.name);if err!=nil{break};_,err=io.Copy(dst,f.file);if err!=nil{break}}
 closeErr:=z.Close();if err!=nil || closeErr!=nil{c.JSON(503,gin.H{"error":"打包失败，请重试"});return}
 if _,err=tmp.Seek(0,0);err!=nil{c.JSON(503,gin.H{"error":"打包失败"});return}
 st,err:=tmp.Stat();if err!=nil{c.JSON(503,gin.H{"error":"打包失败"});return}
 c.Header("Cache-Control","private, no-store")
 c.Header("Content-Type","application/zip")
 c.Header("Content-Disposition",fmt.Sprintf("attachment; filename=M-brand_%s.zip",jid))
 http.ServeContent(c.Writer,c.Request,"download.zip",st.ModTime(),tmp)
}

func contains(arr []string,s string) bool {for _,v:=range arr{if v==s{return true}};return false}
func packForImage(kind string,idx int)string{return packForItemKind(kind)}

// PNG already contains compressed pixel data. Store it losslessly instead of
// spending CPU deflating it again; keep normal compression for other entries.
func createDownloadEntry(z *zip.Writer, name string)(io.Writer,error){
 method:=uint16(zip.Deflate);if strings.EqualFold(filepath.Ext(name),".png"){method=zip.Store}
 return z.CreateHeader(&zip.FileHeader{Name:name,Method:method})
}
