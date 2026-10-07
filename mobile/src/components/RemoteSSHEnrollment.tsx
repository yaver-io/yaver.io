import React, { useEffect, useRef, useState } from "react";
import { Linking, Pressable, ScrollView, Text, View } from "react-native";
import { sshCall } from "../lib/plainSSH";
import { useColors } from "../context/ThemeContext";
import { getWebBaseUrlSync } from "../lib/backendConfig";

/** Target enrollment, not phone sign-in. Separate SSH channel; no runner input. */
export default function RemoteSSHEnrollment({connectionId}:{connectionId:string}) {
 const c=useColors();const [output,setOutput]=useState("");const [busy,setBusy]=useState(false);const [error,setError]=useState("");const alive=useRef(true);
 useEffect(()=>{alive.current=true;return()=>{alive.current=false};},[connectionId]);
 const run=async(op:"enroll"|"installYaver")=>{
  setBusy(true);setError("");setOutput("");
  try {await sshCall({op,id:connectionId});while(alive.current){
   const result=await sshCall<{output:string;done:boolean;error?:string}>({op:"enrollmentRead",id:connectionId});
   if(!alive.current)return;setOutput(previous=>(previous+result.output).slice(-16000));
   if(result.done){setError(result.error||"");break;}await new Promise(resolve=>setTimeout(resolve,750));
  }}catch(cause){if(alive.current)setError(cause instanceof Error?cause.message:"Remote setup failed. Retry over SSH.");}
  finally{if(alive.current)setBusy(false);}
 };
 const link=output.match(/https:\/\/[^\s]+/g)?.find(value=>{try{const u=new URL(value);return u.origin===new URL(getWebBaseUrlSync()).origin && u.pathname.startsWith("/auth/");}catch{return false;}});
 return <View style={{padding:12,gap:8}}>
  <Text style={{color:c.textPrimary,fontWeight:"600"}}>Connect this remote to Yaver</Text>
  <Text style={{color:c.textMuted,fontSize:12}}>Optional. SSH and your runner pane remain available.</Text>
  <Pressable accessibilityRole="button" disabled={busy} onPress={()=>void run("enroll")}><Text style={{color:c.accent}}>{busy?"Remote setup running…":"Sign this remote into Yaver"}</Text></Pressable>
  {!!link && <Pressable accessibilityRole="button" onPress={()=>void Linking.openURL(link)}><Text style={{color:c.accent}}>Approve remote sign-in</Text></Pressable>}
  {output.includes("Yaver is not installed") && <Pressable accessibilityRole="button" disabled={busy} onPress={()=>void run("installYaver")}><Text style={{color:c.accent}}>Install Yaver on this remote</Text></Pressable>}
  {!!output && <ScrollView style={{maxHeight:160}}><Text selectable style={{fontFamily:"monospace",fontSize:12,color:c.textPrimary}}>{output}</Text></ScrollView>}
  {!!error && <Text accessibilityRole="alert" style={{color:c.error}}>{error}</Text>}
 </View>;
}
