import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowLeft, ArrowRight, X } from "lucide-react";
import * as Dialog from "@radix-ui/react-dialog";
import { Button } from "../components/ui/button";
import { api, IMG } from "../lib/api";
import { withBrandExample, type BrandCase } from "../lib/brandExamples";
export default function Gallery(){
 const galleryTrigger=useRef<HTMLButtonElement|null>(null);
 const [remoteCases,setCases]=useState<BrandCase[]|null>(null),[error,setError]=useState(''),[reload,setReload]=useState(0);
 const [view,setView]=useState<{ci:number;ii:number}|null>(null);
 useEffect(()=>{const controller=new AbortController();setError('');api('/api/cases',{signal:controller.signal}).then(data=>{if(!controller.signal.aborted)setCases(data)}).catch(()=>{if(!controller.signal.aborted)setError('案例暂时无法加载，请稍后重试。')});return()=>controller.abort()},[reload]);
 const cases=withBrandExample(remoteCases??[]);
 const current=view&&cases?.[view.ci]?.images[view.ii];
 const change=(step:number)=>{if(!view||!cases?.[view.ci]?.images.length)return;setView({...view,ii:(view.ii+step+cases[view.ci].images.length)%cases[view.ci].images.length})};
 return <main className="page-width"><div className="page-heading"><div><span className="eyebrow">看看品牌可以是什么样子</span><h1>作品案例</h1><p>从独立标识到品牌应用，了解你能获得的成果。</p></div><Button asChild variant="outline"><Link to="/create">创建我的品牌<ArrowRight size={16}/></Link></Button></div>
  {error&&<div className="notice"><p role="alert">{error}</p><Button variant="outline" onClick={()=>setReload(v=>v+1)}>重新加载案例</Button></div>}
  {!remoteCases&&!error&&<p role="status" className="helper-text">正在读取更多案例…</p>}
  {cases.map((c,ci)=><section key={c.id} className={`gallery-section ${c.id==="m-brand-own-example"?"brand-example":""}`}><div className="section-heading"><h2>{c.title}</h2><p>{c.tagline}</p></div><div className="gallery-grid">{c.images.map((im,ii)=><button key={im.file} className="surface gallery-tile" onClick={event=>{galleryTrigger.current=event.currentTarget;setView({ci,ii})}}><picture>{im.avif&&<source type="image/avif" srcSet={IMG(im.avif)}/>}<img src={IMG(im.file)} width={800} height={1000} alt={`${c.title} · ${im.label}`} loading="lazy"/></picture><span>{im.label}</span></button>)}</div></section>)}
  <Dialog.Root open={!!view} onOpenChange={open=>{if(!open)setView(null)}}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="dialog-card image-dialog" aria-describedby={undefined} onCloseAutoFocus={event=>{event.preventDefault();galleryTrigger.current?.focus()}} onKeyDown={e=>{if(e.key==='ArrowRight'){e.preventDefault();change(1)}if(e.key==='ArrowLeft'){e.preventDefault();change(-1)}}}><Dialog.Title>{view&&cases?.[view.ci]?.title} · {current?.label}</Dialog.Title><Dialog.Close className="icon-button dialog-close" aria-label="关闭预览"><X size={20}/></Dialog.Close>{current&&<img src={IMG(current.file)} alt={current.label}/>}<div className="gallery-controls"><Button variant="outline" onClick={()=>change(-1)} aria-label="上一张"><ArrowLeft size={17}/></Button><span>{view?view.ii+1:0} / {view?cases?.[view.ci]?.images.length:0}</span><Button variant="outline" onClick={()=>change(1)} aria-label="下一张"><ArrowRight size={17}/></Button></div></Dialog.Content></Dialog.Portal></Dialog.Root>
 </main>;
}
