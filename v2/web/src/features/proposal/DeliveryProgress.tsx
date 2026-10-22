import {useEffect,useState} from 'react';
import {Check,Clock3,LoaderCircle} from 'lucide-react';
import type {Detail,State} from './useProposal';
export function DeliveryProgress({state,detail}:{state:State;detail:Detail|null}){
 const [now,setNow]=useState(Date.now());
 useEffect(()=>{const timer=setInterval(()=>setNow(Date.now()),1000);return()=>clearInterval(timer)},[]);
 const [anchor,setAnchor]=useState({received:Date.now(),observed:Date.now()});
 useEffect(()=>{setAnchor({received:Date.now(),observed:Date.parse(state.delivery?.observed_at||'')||Date.now()})},[state.delivery?.observed_at]);
 const choosing=['directions','choose'].includes(state.stage),items=detail?.items.filter(i=>i.category!=='free'&&i.purchased)||[];
 const done=choosing?state.images.filter(i=>i.kind==='direction'&&i.file).length:items.filter(i=>i.generated&&i.file&&i.unlocked).length;
 const total=choosing?3:items.length,active=['queued','running','boards_running'].includes(state.status);
 const step=state.delivery?.steps.findLast(s=>s.state==='submitted');
 const label=step?(step.kind==='direction'?`方向 ${'ABC'[step.index]||step.index+1}`:detail?.items.find(i=>i.kind===step.kind)?.name||'品牌内容'):'';
 const elapsed=Math.max(0,Math.floor((anchor.observed+now-anchor.received-(Date.parse(state.delivery?.started_at||'')||anchor.observed))/1000));
 const title=state.delivery?.needs_review?'生成结果正在核对':state.status==='error'?'部分内容需要处理':state.status==='queued'?'提案已受理，等待开始':active?(label?`正在生成：${label}`:'正在制作品牌内容'):choosing?'三个方向已就绪':'已完成内容可以下载';
 if(!choosing&&!active&&!items.length&&state.status!=='error')return null;
 return <section className="surface delivery-progress" aria-label="制作进度">
  <div className="delivery-progress-heading">{active?<LoaderCircle size={20} className="animate-spin" aria-hidden="true"/>:<Check size={20} aria-hidden="true"/>}<div><h2>{title}</h2><p>{choosing?`Logo 方向：已完成 ${done}/3 个`:items.length?`已购内容：已完成 ${done}/${total} 项`:'Logo 组合卡正在准备'}</p></div></div>
  {total>0&&<progress max={total} value={done} aria-label="已完成数量"/>}
  <div className="delivery-progress-meta">{state.delivery?.started_at&&active&&state.status!=='queued'&&<span data-testid="delivery-elapsed"><Clock3 size={14} aria-hidden="true"/>已开始 {Math.floor(elapsed/60)} 分 {elapsed%60} 秒</span>}<span>{items.length?'已购权益已保存':'品牌资料已保存'}</span></div>
  {!choosing&&items.length>0&&<details className="delivery-checklist"><summary>查看制作清单</summary><ul>{items.map(i=><li key={i.kind}><span>{i.name}</span><strong>{i.generated?'已完成':state.delivery?.steps.find(s=>s.kind===i.kind)?.state==='unknown'?'待核对':step?.kind===i.kind?'生成中':'待完成'}</strong></li>)}</ul></details>}
  <p className="helper-text">{state.delivery?.needs_review?'请联系站点管理员 协助核对，无需重复购买。':'可以离开页面，稍后从「我的提案」继续查看。完成的内容会逐项显示。'}</p>
  {active&&state.status!=='queued'&&elapsed>=180&&<p className="helper-text">本次制作耗时较长，已有成果已保留。页面会自动更新，请勿重复提交。</p>}
 </section>
}
