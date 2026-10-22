import {useRef,useState} from "react";
// Single UI command at a time, never retry a mutation automatically.
export function useAction(){const lock=useRef(false),[busy,setBusy]=useState(false),[error,setError]=useState("");async function run(fn:()=>Promise<unknown>){if(lock.current)return;lock.current=true;setBusy(true);setError("");try{await fn()}catch(e){setError(e instanceof Error?e.message:"操作结果暂未确认，请先刷新核对")}finally{lock.current=false;setBusy(false)}}return {run,busy,error}}
