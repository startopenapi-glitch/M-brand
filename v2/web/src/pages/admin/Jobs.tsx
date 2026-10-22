import {useSearchParams} from "react-router-dom";
import {useAction} from "../../features/admin/shared/useAction";
import { useEffect, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { api, IMG } from "../../lib/api";
import { usePoll } from "../../lib/poll";
export default function Jobs() {
 const action=useAction();const [params]=useSearchParams();const wanted=params.get("id");
  const [readError,setReadError]=useState("");
  const [rows, setRows] = useState<any[]>([]);
  const [detail, setDetail] = useState<any>(null);
  const [detailError, setDetailError] = useState("");
  const detailTrigger = useRef<HTMLButtonElement | null>(null);
  const detailRequest = useRef(0);
  useEffect(()=>()=>{detailRequest.current++},[]);
  const load = () => api(`/api/admin/jobs${wanted?`?id=${encodeURIComponent(wanted)}`:""}`).then(r=>{setReadError("");setRows(r)}).catch(e=>setReadError(e.message));
  usePoll(load, 4000);
  const color: Record<string, string> = { done: "text-green-700", error: "text-[#b3261e]", queued: "text-[#666]", running: "text-[var(--accent)]", boards_running: "text-[var(--accent)]", ready_to_choose: "text-[var(--accent)]" };
  return (
    <div>
      {action.busy&&<p role="status" className="text-sm mb-3">正在处理，请勿重复提交…</p>}
      {action.error&&<p role="alert" className="notice notice-error mb-3">{action.error}</p>}
      {readError&&<p role="alert" className="notice notice-error mb-3">{readError}；联网后请刷新核对，已有结果已保留。</p>}
      <h1 className="font-sans text-2xl mb-5">任务监控</h1>
      {detailError && <p role="alert" className="notice notice-error mb-4">{detailError}</p>}
      <div className="surface overflow-x-auto">
        <table className="w-full text-sm">
          <thead><tr className="text-left text-[#666] border-b border-[var(--line)]">
            <th className="p-3">任务</th><th className="p-3">邀请码</th><th className="p-3">状态</th><th className="p-3">阶段</th><th className="p-3">图</th><th className="p-3">创建</th><th className="p-3"></th></tr></thead>
          <tbody>
            {rows.filter(r=>!wanted||r.id===wanted).map((r) => (
              <tr key={r.id} className="border-b border-[var(--soft)]">
                <td className="p-3 font-mono">{r.id}</td>
                <td className="p-3">{r.code}</td>
                <td className={"p-3 font-semibold " + (color[r.status] || "")}>{r.needs_review?"结果待核对":r.status}{r.elapsed_seconds>0&&<span className="block text-xs font-normal">耗时 {Math.floor(r.elapsed_seconds/60)} 分</span>}</td>
                <td className="p-3">{r.stage}</td>
                <td className="p-3">{r.images}</td>
                <td className="p-3 text-[#666]">{r.created_at.slice(5, 16)}</td>
                <td className="p-3 flex gap-2">
                  <button className="ctl-btn-sm" onClick={event => {
                    detailTrigger.current = event.currentTarget; setDetailError("");
                    const request = ++detailRequest.current;
                    api(`/api/admin/jobs/${r.id}`).then(value=>{if(request===detailRequest.current)setDetail(value)})
                      .catch(()=>{if(request===detailRequest.current)setDetailError("任务详情暂时无法加载，请稍后重试。")});
                  }}>详情</button>
                  {r.status === "error" && !r.needs_review && <button className="ctl-btn-sm ctl-btn-sm--accent"
                    onClick={() => action.run(async () => { try{await api(`/api/admin/jobs/${r.id}/retry`, { method: "POST" }); await load()}catch(e){setDetailError(e instanceof Error?e.message:"暂时无法重试，请稍后核对")} })}>重试</button>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <Dialog.Root open={!!detail} onOpenChange={open=>{if(!open){detailRequest.current++;setDetail(null)}}}>
        <Dialog.Portal><Dialog.Overlay className="dialog-overlay"/>
          <Dialog.Content className="dialog-card admin-job-dialog" aria-describedby={undefined} onCloseAutoFocus={event=>{event.preventDefault();detailTrigger.current?.focus()}}>
            <Dialog.Title>任务 {detail?.id} · {detail?.status}</Dialog.Title>
            <Dialog.Close className="icon-button dialog-close" aria-label="关闭任务详情"><X size={20}/></Dialog.Close>
            <p className="text-xs text-[var(--muted)] mt-2 mb-4 break-words">{JSON.stringify(detail?.form)}</p>
            <div className="mb-4">{detail?.delivery?.steps?.map((step:any)=><p className="text-sm my-2" key={`${step.kind}-${step.index}`}>{step.kind} · {({submitted:"正在执行 / 结果待核对",unknown:"结果待核对，请先核验原请求",done:"已完成",failed:"明确失败"} as Record<string,string>)[step.state]||step.state}{step.finished_at&&` · ${Math.max(0,Math.round((Date.parse(step.finished_at)-Date.parse(step.started_at))/1000))} 秒`}</p>)}</div>
            <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill,minmax(150px,1fr))" }}>
              {detail?.images?.map((im: any, i: number) => (
                <div key={i} className="border border-[var(--line)] p-2">
                  <img src={IMG(im.file)} alt={`${im.kind} · ${im.idx}`} className="w-full aspect-[4/5] object-contain" />
                  <p className="text-[11px] text-[#666] mt-1">{im.kind}#{im.idx}</p>
                </div>
              ))}
            </div>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </div>
  );
}
