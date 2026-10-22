import {useEffect,useState} from 'react';
import {Link,useSearchParams} from 'react-router-dom';
import {api} from '../lib/api';
// Old bookmarks are a read-only order lookup, never a second purchase flow.
export default function PayPage(){
 const [search]=useSearchParams();const [order,setOrder]=useState<any>(null);const [error,setError]=useState('');
 useEffect(()=>{let stop=false;let id=search.get('order')||'';if(!id){try{id=JSON.parse(localStorage.getItem('zb_last_order')||'null')?.id||''}catch{}}
 if(id)api(`/api/pay/order/${encodeURIComponent(id)}`).then(o=>{if(!stop)setOrder(o)}).catch(()=>{if(!stop)setError('暂时无法查询订单，请到我的订单核对。')});return()=>{stop=true}},[search]);
 return <main className="mx-auto max-w-xl px-5 py-16"><h1 className="font-sans text-2xl">在提案内选择并付款</h1><p className="my-4">付款入口已统一到品牌提案的项目清单，选择需要的内容后再付款。</p>{error&&<p role="alert">{error}</p>}{order&&<section className="my-5 border bg-white p-4"><p className="break-all">订单 {order.id}</p><p>¥{(order.amount_cents/100).toFixed(2)} · {order.status==='paid'?'已支付':'待核对'}</p>{order.job_id&&<Link className="underline" to={`/job/${order.job_id}`}>返回提案</Link>}</section>}<div className="flex flex-wrap gap-4"><Link className="underline" to="/orders">我的提案与订单</Link><Link className="underline" to={search.get('job')?`/job/${encodeURIComponent(search.get('job')!)}`:'/create'}>进入提案</Link></div></main>
}
