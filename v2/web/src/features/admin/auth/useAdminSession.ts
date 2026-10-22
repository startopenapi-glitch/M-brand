import {useCallback,useEffect,useRef,useState} from "react";
import {api} from "../../../lib/api";
export type AdminSession={name:string;capabilities:string[]};
export function useAdminSession(){
 const [session,setSession]=useState<AdminSession|null>(null),[status,setStatus]=useState("loading");const seq=useRef(0);
 const retry=useCallback(async()=>{const id=++seq.current;setStatus("loading");try{const s=await api("/api/admin/session");if(id!==seq.current)return;setSession(s);setStatus("ready")}catch(e:any){if(id!==seq.current)return;setStatus(e.status===401||e.status===403&&e.data?.error==="未登录管理员"?"anonymous":e.status===403?"denied":"error")}},[]);
 useEffect(()=>{retry();return()=>{seq.current++}},[retry]);return {session,status,retry};
}
