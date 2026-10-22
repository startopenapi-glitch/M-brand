package app

import (
 "fmt"
 "os"
 "path/filepath"
 "strings"
)

// The single pipeline: system card first, followed only by entitled items.
// Entitlements are re-read per run; completed original files are reused after restart.
func stageBoardsV3(j jobRow) error {
 var version int64
 if err:=pool.QueryRow(contextBG(),"SELECT delivery_version FROM jobs WHERE id=$1",j.ID).Scan(&version);err!=nil{return err}
 if e:=markLegacyFulfilled(j.ID);e!=nil{return e}
 rights,err:=jobEntitlements(contextBG(),pool,j.ID);if err!=nil{return err}
 if err=os.MkdirAll(jobDir(j.ID),0755);err!=nil{return err}
 var failures []string
 for _,spec:=range itemSpecs(j.Form){
  if e:=checkExecution(j);e!=nil{return e}
  if !rights[spec.Kind]{continue}
  if _,err=pool.Exec(contextBG(),`INSERT INTO delivery_items(job_id,kind,status) VALUES($1,$2,'running') ON CONFLICT(job_id,kind) DO UPDATE SET status='running',updated_at=now()`,j.ID,spec.Kind);err!=nil{return err}
  err=generateItem(j,spec)
  if e:=checkExecution(j);e!=nil{return e}
  state:="done";if err!=nil{state="error";failures=append(failures,spec.Kind+": "+err.Error())}
  if _,e:=pool.Exec(contextBG(),"UPDATE delivery_items SET status=$3,updated_at=now() WHERE job_id=$1 AND kind=$2",j.ID,spec.Kind,state);e!=nil{return e}
  // A missing system card invalidates all dependent generation; stop before paid calls.
  if err!=nil && spec.Kind=="system_card" {return fmt.Errorf("Logo 组合卡未完成: %w",err)}
 }
 if e:=markLegacyFulfilled(j.ID);e!=nil{return e}
 if len(failures)>0{return fmt.Errorf("部分交付未完成: %s",strings.Join(failures,"; "))}
 tag,err:=pool.Exec(contextBG(),`UPDATE jobs SET status=CASE WHEN delivery_version=$2 THEN 'done' ELSE 'queued' END,
 stage=CASE WHEN delivery_version=$2 THEN 'done' ELSE 'boards' END,updated_at=now() WHERE id=$1 AND ($3='' OR execution_token=$3)`,j.ID,version,j.ExecutionToken)
 if err!=nil{return err};if tag.RowsAffected()!=1{return fmt.Errorf("任务不存在")};return nil
}

func generateItem(j jobRow,spec ItemSpec)error {
 if f:=priorBoardFile(contextBG(),pool,j.ID,spec.Kind);f!="" {if opened,e:=openArtifact(j.ID,f);e==nil{opened.Close();return nil}}
 if spec.Kind=="brand_story" {
  path:=filepath.Join(jobDir(j.ID),"brand_story.md")
  raw,err:=os.ReadFile(path)
  if err!=nil && !os.IsNotExist(err){return err}
  if len(raw)==0 || strings.Contains(string(raw),"【任务】撰写品牌故事文档") {
   if err=os.WriteFile(path,[]byte(brandStory(j.Form)),0644);err!=nil{return err}
  }
  return recordImage(j.ID,j.Code,spec.Kind,spec.Priority,webPath(j.ID,"brand_story.md"),0,"",j.ExecutionToken)
 }
 // The reference order and responsibilities are centralised in referenceInputs.
 refsInput:=referenceInputs(j,spec.Kind)
 refs:=make([]string,0,len(refsInput))
 for _,r:=range refsInput{refs=append(refs,r.Path)}
 snap,snapOK:=selectedSnapshot(j)
 var snapPtr *SelectedDirectionSnapshot
 if snapOK{snapPtr=&snap}
 prompt:=buildItemPromptWithContext(j.Form,spec,snapPtr,refsInput)
 // Existing valid file can be registered even if reference files disappeared.
 dest:=filepath.Join(jobDir(j.ID),spec.Kind+".png")
 if raw,err:=os.ReadFile(dest);err==nil{if _,e:=imagePNG(raw);e==nil{return ensureJobImageSize(j,spec.Kind,spec.Priority,spec.Kind+".png",prompt,nil,itemSize(spec))}}
 return ensureJobImageSize(j,spec.Kind,spec.Priority,spec.Kind+".png",prompt,refs,itemSize(spec))
}
func itemSize(s ItemSpec)string{if s.IsSquare{return "1024x1024"};return "1024x1280"}

// A usable editorial document assembled only from the customer's stated facts.
// It does not assert invented founding dates, sales, certifications or awards.
func brandStory(f BrandForm)string{
 name:=strings.TrimSpace(f["cn_name"]);if name==""{name="品牌"}
 var b strings.Builder
 fmt.Fprintf(&b,"# %s：品牌故事\n\n%s专注于%s。\n\n",name,name,f["industry"])
 if v:=strings.TrimSpace(f["products"]);v!=""{fmt.Fprintf(&b,"## 我们提供什么\n\n我们围绕%s，持续完善产品与服务。\n\n",v)}
 if v:=strings.TrimSpace(f["positioning"]);v!=""{fmt.Fprintf(&b,"## 为谁而做\n\n%s面向%s，希望让每一次选择都更贴近真实需要。\n\n",name,v)}
 if v:=strings.TrimSpace(f["philosophy"]);v!=""{fmt.Fprintf(&b,"## 我们的坚持\n\n%s\n\n这一理念是我们在产品、服务与表达上持续打磨的方向。\n\n",v)}
 if v:=strings.TrimSpace(f["slogan"]);v!=""{fmt.Fprintf(&b,"## 一句话表达\n\n%s\n\n",v)}
 fmt.Fprintf(&b,"## 与你一起\n\n%s希望通过清晰、一致的品牌表达，让人们理解我们提供的价值，并在日常体验中建立信任。\n",name)
 return b.String()
}
