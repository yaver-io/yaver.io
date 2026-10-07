import React, { useEffect, useRef, useState } from "react";
import { Pressable, ScrollView, Text, View } from "react-native";
import { sshCall } from "../lib/plainSSH";
import { useColors } from "../context/ThemeContext";
import { approveDeviceCode, fetchDeviceCodeInfoResult, DeviceCodeInfo } from "../lib/deviceCodeApprove";
import { confirmDeviceApprovalPresence } from "../lib/deviceApprovalPresence";
import { useAuth } from "../context/AuthContext";
import { getWebBaseUrlSync } from "../lib/backendConfig";

/** Target enrollment, not phone sign-in. Separate SSH channel; no runner input. */
export default function RemoteSSHEnrollment({connectionId}:{connectionId:string}) {
 const {token}=useAuth();
 const [request,setRequest]=useState<{code:string;info:DeviceCodeInfo}|null>(null);
 const [approving,setApproving]=useState(false);
 const c=useColors();const [output,setOutput]=useState("");const [busy,setBusy]=useState(false);const [error,setError]=useState("");const alive=useRef(true);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false};},[connectionId]);
 const run=async(op:"enroll"|"installYaver")=>{
  setBusy(true);setError("");setOutput("");setRequest(null);
  try {await sshCall({op,id:connectionId});while(alive.current){
   const result=await sshCall<{output:string;done:boolean;error?:string}>({op:"enrollmentRead",id:connectionId});
   if(!alive.current)return;setOutput(previous=>(previous+result.output).slice(-16000));
   if(result.done){setError(result.error||"");break;}await new Promise(resolve=>setTimeout(resolve,750));
  }}catch(cause){if(alive.current)setError(cause instanceof Error?cause.message:"Remote setup failed. Retry over SSH.");}
  finally{if(alive.current)setBusy(false);}
 };
 const link=output.match(/https:\/\/[^\s]+/g)?.find(value=>{try{const u=new URL(value);return u.origin===new URL(getWebBaseUrlSync()).origin && u.pathname.startsWith("/auth/");}catch{return false;}});
 const approve=async()=>{
  if(!link)return;
  if(!token){setError("Connect your Yaver account in the card above, then reconnect SSH and retry remote setup. You can also approve the displayed code on another signed-in device.");return;}
  const code=new URL(link).searchParams.get("code")||"";
  const clean=code.toUpperCase().replace(/[^A-Z0-9]/g,"");if(clean.length!==8){setError("Use the displayed approval link on another signed-in device.");return;}
  setApproving(true);
  try {
   const formatted=clean.slice(0,4)+"-"+clean.slice(4);
   if(!request || request.code!==formatted){
    const result=await fetchDeviceCodeInfoResult(formatted);
    if(result.kind!=="found")throw new Error(result.kind==="unreachable"?result.message:"This request expired. Start remote sign-in again.");
    if(result.info.status!=="pending" || !result.info.expiresAt || result.info.expiresAt<=Date.now())throw new Error("This request is no longer pending. Start remote sign-in again.");
    setRequest({code:formatted,info:result.info});return;
   }
   await confirmDeviceApprovalPresence(request.info.machineName || "this remote");
   const result=await approveDeviceCode(formatted,token);
   if(!result.ok)throw new Error(result.error);setRequest(null);setError("");
  }catch(cause){setError(cause instanceof Error?cause.message:"Yaver approval failed.");}finally{setApproving(false);}
 };
 return <View style={{padding:12,gap:8}}>
  <Text style={{color:c.textPrimary,fontWeight:"600"}}>Connect this remote to Yaver</Text>
  <Text style={{color:c.textMuted,fontSize:12}}>Optional. SSH and your runner pane remain available.</Text>
  <Pressable accessibilityRole="button" disabled={busy} onPress={()=>void run("enroll")}><Text style={{color:c.accent}}>{busy?"Remote setup running…":"Sign this remote into Yaver"}</Text></Pressable>
  {request && <Text style={{color:c.textPrimary}}>Approve {request.info.machineName || "this device"} ({request.info.platform || "unknown platform"})? Match code {request.code} on the requesting device. It will receive its own Yaver session.</Text>}
  {!!link && <Pressable accessibilityRole="button" disabled={approving} onPress={()=>void approve()}><Text style={{color:c.accent}}>{request?"Confirm remote sign-in":"Review remote sign-in"}</Text></Pressable>}
  {output.includes("Yaver is not installed") && <Pressable accessibilityRole="button" disabled={busy} onPress={()=>void run("installYaver")}><Text style={{color:c.accent}}>Install Yaver on this remote</Text></Pressable>}
  {!!output && <ScrollView style={{maxHeight:160}}><Text selectable style={{fontFamily:"monospace",fontSize:12,color:c.textPrimary}}>{output}</Text></ScrollView>}
  {!!error && <Text accessibilityRole="alert" style={{color:c.error}}>{error}</Text>}
 </View>;
}
