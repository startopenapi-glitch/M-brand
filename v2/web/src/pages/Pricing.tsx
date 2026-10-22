import { useEffect,useState } from "react";
import { Link } from "react-router-dom";
import { Check, ArrowRight } from "lucide-react";
import { api } from "../lib/api";
import { Button } from "../components/ui/button";
export default function Pricing(){
 const [data,setData]=useState<{items:{kind:string;name:string;price_cents:number}[];bundle_price:number}|null>(null),[error,setError]=useState(''),[reload,setReload]=useState(0);
 useEffect(()=>{const c=new AbortController();setError('');api('/api/items',{signal:c.signal}).then(setData).catch(()=>{if(!c.signal.aborted)setError('价格暂不可用，请稍后刷新。')});return()=>c.abort()},[reload]);
 return <main className="page-width pricing-page"><div className="page-heading"><div><span className="eyebrow">先解锁方向，再决定</span><h1>9.9 元解锁自己的品牌，再按需购买</h1><p>没有必须购买的整套套餐，选择适合当前阶段的交付内容。</p></div></div><div className="pricing-layout"><section className="surface free-benefits"><span className="eyebrow">每个新品牌</span><h2>¥9.90 解锁自己的品牌。</h2><ul><li><Check size={17}/>3 个 Logo 方向</li><li><Check size={17}/>选定方向后的 Logo 组合卡（Symbol + 中英文标准字）</li><li><Check size={17}/>保留提案，随时回来查看</li></ul><Button asChild size="lg" className="w-full"><Link to="/create">9.9 元解锁自己的品牌<ArrowRight size={16}/></Link></Button><p className="helper-text">9.9 元解锁自己的品牌（3 个 Logo 方向 + Logo 组合卡）；17 个付费交付项在提案内选择。</p></section><section className="surface pricing-items"><div className="pricing-items-heading"><h2>按需选择交付项</h2>{data&&<p>全选封顶 <strong>¥{(data.bundle_price/100).toFixed(2)}</strong></p>}</div>{error?<div className="p-6"><p role="alert" className="notice notice-error">{error}</p><Button variant="outline" className="mt-4" onClick={()=>setReload(v=>v+1)}>重新读取价格</Button></div>:data?<ul>{data.items.map(it=><li key={it.kind}><span>{it.name}</span><strong>¥{(it.price_cents/100).toFixed(2)}</strong></li>)}</ul>:<p role="status" className="loading-line">正在读取价格…</p>}</section></div><p className="helper-text mt-6">以付款页面核验的实际金额为准。已购内容不重复购买；本服务不支持退款，请确认所选项目后付款。</p></main>;
}
