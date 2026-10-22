import { useEffect, useRef, useState, type MouseEvent } from "react";
import { Download, LoaderCircle } from "lucide-react";
import { Button } from "../../components/ui/button";

export function DeliveryDownload({ id, count }: { id: string; count?:number }) {
  const request = useRef<AbortController | null>(null);
  const [busy, setBusy] = useState(false), [error, setError] = useState("");
  useEffect(() => () => { request.current?.abort(); request.current = null; }, [id]);
  async function download(event: MouseEvent<HTMLAnchorElement>) {
    event.preventDefault();
    if (request.current) return;
    const controller = new AbortController(); request.current = controller; setBusy(true); setError("");
    const timer = setTimeout(() => controller.abort(), 120000);
    try {
      const response = await fetch(`/api/jobs/${encodeURIComponent(id)}/download`, { credentials: "include", signal: controller.signal });
      if (!response.ok || !response.headers.get("Content-Type")?.includes("application/zip")) throw new Error("Download unavailable");
      const blob = await response.blob();
      if (controller.signal.aborted) throw new Error("Download interrupted");
      const url = URL.createObjectURL(blob), link = document.createElement("a");
      link.href = url; link.download = `M-brand-${id}.zip`; document.body.appendChild(link); link.click(); link.remove();
      // Safari may consume the blob asynchronously after the click.
      setTimeout(() => URL.revokeObjectURL(url), 60000);
    } catch {
      if (request.current === controller) setError("下载暂时失败，请稍后重试。已购内容和权益已保留。");
    } finally {
      clearTimeout(timer);
      if (request.current === controller) { request.current = null; setBusy(false); }
    }
  }
  return <div className="mt-5">
    {error && <p role="alert" className="notice notice-error mb-3">{error}</p>}
    <Button asChild><a href={`/api/jobs/${encodeURIComponent(id)}/download`} onClick={download} aria-disabled={busy} aria-busy={busy}>
      {busy ? <LoaderCircle size={16} className="animate-spin"/> : <Download size={16}/>}{busy ? "正在准备下载…" : count!==undefined?`下载已完成的 ${count} 项`:"下载已购内容 ZIP"}
    </a></Button>
  </div>;
}
