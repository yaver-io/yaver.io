"use client";
import React,{useEffect,useRef,useState} from "react";
import Link from "next/link";
import "@xterm/xterm/css/xterm.css";

interface Pane {id:string;sessionId:string;identity:string;session:string;window:string;command:string;width:number;height:number}
/** Account-independent desktop/browser client. Cloud middleware must never
 * gate this page. SSH performs its own host and OS-user authentication. */
export default function SSHPage(){
 const [host,setHost]=useState("");const [port,setPort]=useState("22");const [user,setUser]=useState("");const [password,setPassword]=useState("");const [privateKey,setPrivateKey]=useState("");const [passphrase,setPassphrase]=useState("");
 const [fingerprint,setFingerprint]=useState("");const [panes,setPanes]=useState<Pane[]|null>(null);const [pane,setPane]=useState<Pane|null>(null);const [error,setError]=useState("");const [status,setStatus]=useState("");const [busy,setBusy]=useState(false);const [native,setNative]=useState(false);const [companion,setCompanion]=useState(false);
 const mount=useRef<HTMLDivElement>(null);const terminal=useRef<any>(null);const id=useRef("");const generation=useRef(0);const queue=useRef(Promise.resolve());
 const invoke=async<T,>(request:Record<string,unknown>):Promise<T>=>{
  const desktop=(window as any).yaver?.plainSSH;
  let result;
  if(desktop)result=await desktop(request);
  else {const abort=new AbortController();const timer=setTimeout(()=>abort.abort(),45000);try{const response=await fetch("http://127.0.0.1:18494/invoke",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(request),signal:abort.signal});if(!response.ok)throw new Error("Local SSH companion rejected this origin. Start it for this exact page origin.");result=await response.json();}finally{clearTimeout(timer);}}
  if(!result.ok)throw new Error(result.error?.message||"SSH operation failed");return result.value as T;
 };
 const report=(e:unknown)=>setError(e instanceof Error?e.message:"SSH failed. Check the connection and retry.");
 const detach=()=>{generation.current++;const connection=id.current;id.current="";if(connection)void invoke({op:"close",id:connection}).catch(()=>{});setPane(null);setPanes(null);setStatus("Closed · remote panes keep running");};
 useEffect(()=>{setNative(!!(window as any).yaver?.plainSSH);return()=>{generation.current++;const connection=id.current;if(connection)void invoke({op:"close",id:connection}).catch(()=>{});};},[]);
 const encoded=(text:string)=>{let binary="";for(const b of new TextEncoder().encode(text))binary+=String.fromCharCode(b);return btoa(binary)};
 const send=(text:string,op="write")=>{const connection=id.current;const work=queue.current.then(async()=>{if(!connection||connection!==id.current)throw new Error("Reconnect before sending input.");await invoke({op,id:connection,data:encoded(text)});});queue.current=work.catch(report);return work;};
 useEffect(()=>{
  if(!pane||!mount.current)return;let cancelled=false;const attempt=generation.current;
  void import("@xterm/xterm").then(async({Terminal})=>{if(cancelled||!mount.current)return;const term=new Terminal({cols:pane.width,rows:pane.height,fontSize:13,cursorBlink:true,theme:{background:"#0b0e14",foreground:"#d7dce5"}});term.open(mount.current);terminal.current=term;term.focus();term.onData(text=>void send(text).catch(()=>{}));
   try{while(!cancelled && generation.current===attempt){const frame=await invoke<{data:string}>({op:"read",id:id.current});if(!cancelled&&frame.data)term.write(Uint8Array.from(atob(frame.data),c=>c.charCodeAt(0)));}}catch(e){if(!cancelled){report(e);setStatus("Disconnected · reconnect to resume");}}
  }).catch(report);
  return()=>{cancelled=true;terminal.current?.dispose();terminal.current=null;};
 },[pane]);
 const run=async(fn:()=>Promise<void>)=>{setBusy(true);setError("");try{await fn();}catch(e){report(e);}finally{setBusy(false);}};
 const field=(label:string,value:string,set:(v:string)=>void,secret=false)=><input aria-label={label} placeholder={label} value={value} type={secret?"password":"text"} onChange={e=>{set(e.target.value);setFingerprint("");}} className="w-full rounded-lg border border-white/20 bg-transparent p-3"/>;
 if(pane)return <main className="relative h-dvh w-full overflow-hidden bg-[#0b0e14] text-gray-100">
  <div ref={mount} data-testid="plain-ssh-terminal" className="absolute inset-0 overflow-hidden"/>
  <div className="absolute right-2 top-2 z-10 flex items-center gap-2 rounded-md border border-white/15 bg-black/80 px-2 py-1 text-xs shadow-lg backdrop-blur">
   <span className="max-w-56 truncate text-gray-400">{pane.session} · {pane.window} · {pane.id}</span>
   <button aria-label="Close pane" onClick={detach} className="rounded px-2 py-1 text-gray-200 hover:bg-white/10">Close</button>
  </div>
  {error&&<p role="alert" className="absolute bottom-2 left-2 right-2 z-10 rounded bg-red-950/90 px-3 py-2 text-sm text-red-200">{error}</p>}
 </main>;
 return <main className="flex h-dvh flex-col bg-black text-gray-100">
  <header className="flex items-center gap-4 p-4"><Link href="/dashboard" onClick={detach}>‹ Back</Link><h1 className="flex-1 font-semibold">Plain SSH</h1></header>
  {status&&<p className="px-4 text-xs text-gray-400">{status}</p>}{error&&<p role="alert" className="px-4 text-sm text-red-300">{error}</p>}
  <section className="mx-auto w-full max-w-xl space-y-3 overflow-auto p-4">
   {!native&&!companion?<><p>SSH works without Yaver sign-in in the desktop or mobile app. Browsers need a local SSH companion.</p><button onClick={()=>setCompanion(true)} className="rounded border p-3">Use local SSH companion</button><p className="text-xs text-gray-400">Start the companion with this page's exact origin; it listens only on your computer. No SSH credentials are stored in the browser.</p><code>npm run ssh:bridge -- --origin {typeof location!=="undefined"?location.origin:"https://yaver.io"}</code></>:panes?<>{panes.map(p=><button className="block w-full rounded border border-white/20 p-3 text-left" key={p.identity} disabled={busy} onClick={()=>void run(async()=>{await invoke({op:"open",id:id.current,pane:p.id,session:p.sessionId,identity:p.identity});setPane(p);setStatus(`${p.session} · ${p.window} · ${p.id}`);})}>{p.session} · {p.window} · {p.id} · {p.command}</button>)}{!panes.length&&<p>No tmux panes. Run tmux on this SSH account, then reconnect.</p>}</>:<>
    <p className="text-sm text-gray-400">Connect over SSH or Tailscale. Your remote does not need a Yaver account.</p>{field("Hostname or Tailscale address",host,setHost)}{field("SSH port",port,setPort)}{field("SSH username",user,setUser)}{field("SSH password (optional with a key or Tailscale SSH)",password,setPassword,true)}
    <details><summary>SSH private key</summary><textarea aria-label="SSH private key" value={privateKey} onChange={e=>setPrivateKey(e.target.value)} className="w-full bg-transparent p-2"/>{field("Key passphrase",passphrase,setPassphrase,true)}</details>
    {fingerprint?<><p>Verify this host key on your machine:</p><code className="break-all">{fingerprint}</code><button className="block rounded border p-3" disabled={busy} onClick={()=>void run(async()=>{const attempt=generation.current;const result=await invoke<{id:string}>({op:"connect",host,port:Number(port),user,password,privateKey,passphrase,fingerprint});if(attempt!==generation.current){await invoke({op:"close",id:result.id});return;}id.current=result.id;setPassword("");setPrivateKey("");setPassphrase("");const available=await invoke<Pane[]>({op:"panes",id:result.id});if(attempt!==generation.current)return;setPanes(available);setStatus("Connected over SSH");})}>Trust host and connect</button></>:<button className="rounded border p-3" disabled={busy||!host||!user} onClick={()=>void run(async()=>{if(!Number.isInteger(Number(port))||Number(port)<1||Number(port)>65535)throw new Error("Enter a valid SSH port.");const result=await invoke<{fingerprint:string}>({op:"probe",host,port:Number(port),user});setFingerprint(result.fingerprint);})}>Check host key</button>}
   </>}
  </section>
 </main>;
}
