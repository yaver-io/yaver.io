import React, { forwardRef, useEffect, useImperativeHandle, useRef, useState } from "react";
import { Text, View } from "react-native";
import type { XtermHandle, XtermViewProps } from "../lib/xtermTypes";
import "@xterm/xterm/css/xterm.css";
export type { XtermHandle, XtermViewProps } from "../lib/xtermTypes";

// Browser VT rendering is independent of transport. The caller supplies an
// authorized agent WebSocket or the explicitly enabled local SSH companion.
const XtermViewWeb = forwardRef<XtermHandle, XtermViewProps>(function XtermViewWeb(props, ref) {
  const mount = useRef<HTMLDivElement | null>(null);
  const term = useRef<any>(null);
  const fit = useRef<any>(null);
  const latest = useRef(props); latest.current = props;
  const pending = useRef<Uint8Array[]>([]);
  const [failure,setFailure] = useState("");
  useImperativeHandle(ref, () => ({
    write(bytes) { if(term.current) term.current.write(bytes); else if(pending.current.reduce((n,b)=>n+b.length,0)+bytes.length<=1024*1024) pending.current.push(bytes); else {const message="Terminal output buffer filled. Reopen the terminal.";setFailure(message);latest.current.onError?.(message);} },
    reset() { term.current?.reset(); },
    submit(text) {term.current?.paste(text);term.current?.input("\r",true);},
    focus() { term.current?.focus(); },
    fit() { if(!latest.current.columns) fit.current?.fit(); },
    setFontSize(size) { if(term.current) {term.current.options.fontSize=Math.max(9,Math.min(28,size));if(!latest.current.columns) fit.current?.fit();} },
  }), []);
  useEffect(() => {
    let cancelled=false; let observer: ResizeObserver | undefined; let timer: ReturnType<typeof setTimeout> | undefined;
    void Promise.all([import("@xterm/xterm"),import("@xterm/addon-fit")]).then(([{Terminal},{FitAddon}]) => {
      if(cancelled || !mount.current) return;
      const t=new Terminal({fontSize:latest.current.fontSize || 13,cursorBlink:true,theme:{background:latest.current.background || "#0b0e14",foreground:latest.current.foreground || "#d7dce5"}});
      const f=new FitAddon();t.loadAddon(f);t.open(mount.current!);term.current=t;fit.current=f;
      const resize=()=>{const p=latest.current;if(p.columns&&p.rows)t.resize(p.columns,p.rows);else f.fit();p.onResize?.(t.cols,t.rows);};
      resize();observer=new ResizeObserver(resize);observer.observe(mount.current);
      t.onData((data)=>latest.current.onData?.(new TextEncoder().encode(data)));
      t.onWriteParsed(()=>{if(timer)return;timer=setTimeout(()=>{timer=undefined;const buffer=t.buffer.active;const lines=[];for(let i=Math.max(0,buffer.length-120);i<buffer.length;i++)lines.push(buffer.getLine(i)?.translateToString(true)||"");latest.current.onScreen?.(lines.join("\n").trim().slice(-24000));},180);});
      for(const bytes of pending.current)t.write(bytes);pending.current=[];
      latest.current.onReady?.();
    }).catch(()=>{if(!cancelled){const message="Terminal could not load. Reload this screen to retry.";setFailure(message);latest.current.onError?.(message);}});
    return ()=>{cancelled=true;observer?.disconnect();if(timer)clearTimeout(timer);term.current?.dispose();term.current=null;};
  }, []);
  return <View style={[{flex:1},props.style]}>{!!failure && <Text accessibilityRole="alert" style={{color:"#fca5a5",padding:12}}>{failure}</Text>}<div ref={mount} style={{height:"100%",width:"100%",overflow:"auto",background:props.background||"#0b0e14"}} /></View>;
});
export default XtermViewWeb;
