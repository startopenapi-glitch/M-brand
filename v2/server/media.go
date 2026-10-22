package app
import ("encoding/json";"net/http";"path/filepath";"strings";"github.com/gin-gonic/gin")
func serveGenerated(c *gin.Context){
 c.Header("Cache-Control","private, no-cache");c.Header("X-Content-Type-Options","nosniff");c.Header("Vary","Accept")
 deny:=func(){c.Status(http.StatusNotFound)}
 rel:=strings.TrimPrefix(c.Param("filepath"),"/");parts:=strings.Split(rel,"/")
 if len(parts)!=2 || parts[0]=="" || parts[0]=="." || parts[0]==".." || strings.ContainsAny(rel,"\\\x00"){deny();return}
 path:="gen/"+rel;needle,_:=json.Marshal([]map[string]string{{"file":path}});var published bool
 if pool.QueryRow(c,"SELECT EXISTS(SELECT 1 FROM cases WHERE published=true AND images @> $1::jsonb)",string(needle)).Scan(&published)!=nil{deny();return}
 if !published{
  var owner,code string
  if pool.QueryRow(c,`SELECT COALESCE(visitor,''),COALESCE(NULLIF(quota_code,''),code,'') FROM jobs WHERE id=$1`,parts[0]).Scan(&owner,&code)!=nil || !jobOwned(c,visitorID(c),"",owner,code){deny();return}
  var kind string;var idx int
  // PNG/WebP variants belong to the same persisted artifact, not an arbitrary same-directory file.
  stem:=strings.TrimSuffix(path,filepath.Ext(path))
  if pool.QueryRow(c,`SELECT kind,idx FROM job_images WHERE job_id=$1 AND (file=$2 OR file=$3 OR file=$4) LIMIT 1`,parts[0],path,stem+".png",stem+".webp").Scan(&kind,&idx)!=nil{deny();return}
  rights,err:=jobEntitlements(c,pool,parts[0]);if err!=nil || !rights[artifactKind(kind,idx)]{deny();return}
 }
 f,err:=openArtifact(parts[0],path);if err!=nil{deny();return};defer f.Close()
 if (strings.HasSuffix(path,".png") || strings.HasSuffix(path,".webp")) && strings.Contains(c.GetHeader("Accept"),"image/webp"){
  wp:=strings.TrimSuffix(path,filepath.Ext(path))+".webp"
  if wf,e:=openArtifact(parts[0],wp);e==nil{defer wf.Close();wi,e:=wf.Stat();if e==nil{c.Header("Content-Type","image/webp");http.ServeContent(c.Writer,c.Request,filepath.Base(wp),wi.ModTime(),wf);return}}
 }
 st,err:=f.Stat();if err!=nil{deny();return}
 if strings.HasSuffix(path,".md"){c.Header("Content-Type","text/plain; charset=utf-8")}
 http.ServeContent(c.Writer,c.Request,st.Name(),st.ModTime(),f)
}
