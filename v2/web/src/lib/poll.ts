import { useEffect, useRef } from "react";
/** Serial read polling. Wake events coalesce; mutation callers must not use this hook. */
export function usePoll(fn:()=>void|Promise<unknown>,ms:number){
 const ref=useRef(fn);ref.current=fn;
 useEffect(()=>{let timer:ReturnType<typeof setTimeout>|undefined,stopped=false,running=false,failures=0;
 const tick=async()=>{if(stopped||running)return;if(timer)clearTimeout(timer);if(document.hidden||!navigator.onLine)return;running=true;
 try{await ref.current();failures=0}catch{failures=Math.min(failures+1,3)}finally{running=false;if(!stopped)timer=setTimeout(tick,ms*Math.pow(2,failures))}};
 const wake=()=>{if(!document.hidden&&navigator.onLine)void tick()};void tick();
 document.addEventListener("visibilitychange",wake);window.addEventListener("online",wake);
 return()=>{stopped=true;if(timer)clearTimeout(timer);document.removeEventListener("visibilitychange",wake);window.removeEventListener("online",wake)};
 },[ms]);
}
