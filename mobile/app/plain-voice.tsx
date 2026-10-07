import React,{useEffect,useRef,useState} from "react";
import {NativeModules,Pressable,Text,View} from "react-native";
import {SafeAreaView} from "react-native-safe-area-context";
import {useRouter} from "expo-router";
import * as Speech from "expo-speech";
import {startRealtimeTranscribe} from "../src/lib/speech";
import {loadSSHVoiceTarget,saveSSHVoiceTarget,sendSSHVoice} from "../src/lib/plainSSHVoice";
import {useColors} from "../src/context/ThemeContext";

/** CarPlay stays voice-only: no code/logs on the car display. Every SSH
 * submission gets a spoken confirmation, even when the text sounds harmless. */
export default function PlainVoice(){
 const c=useColors();const router=useRouter();const [status,setStatus]=useState("Ready for your SSH pane");const [busy,setBusy]=useState(false);const alive=useRef(true);const recording=useRef<{stop:()=>Promise<string>}|null>(null);
 const state=(value:string)=>{NativeModules.YaverInfo?.setCarPlayVoiceState?.(value);};
 const speak=(text:string)=>new Promise<void>(resolve=>{state("speaking");Speech.speak(text,{onDone:resolve,onStopped:resolve,onError:()=>resolve()});});
 const listen=async(ms:number)=>{state("listening");const rec=await startRealtimeTranscribe(()=>{});if(!alive.current){await rec.stop();return "";}recording.current=rec;await new Promise(resolve=>setTimeout(resolve,ms));if(!alive.current)return "";recording.current=null;return (await rec.stop()).trim();};
 const turn=async()=>{if(busy)return;setBusy(true);try{
  if(!await loadSSHVoiceTarget())throw new Error("Choose a pane for CarPlay in Plain SSH on your phone.");
  setStatus("Listening…");await speak("What would you like to send to your SSH pane?");if(!alive.current)return;
  const text=await listen(7000);if(!alive.current||!text)return;
  setStatus("Waiting for spoken confirmation");await speak("Send that message to your selected pane? Say send or cancel.");if(!alive.current)return;
  const confirmation=(await listen(4000)).toLowerCase().replace(/[.!?]/g,"").trim();if(!alive.current)return;
  if(!["send","yes send","send it"].includes(confirmation)){setStatus("Cancelled");await speak("Cancelled. Nothing was sent.");return;}
  state("working");setStatus("Sending over SSH…");await sendSSHVoice(text);if(alive.current){setStatus("Sent to your existing pane");await speak("Sent to your pane. Your runner is continuing on the remote.");}
 }catch(error){if(alive.current){const message=error instanceof Error?error.message:"SSH failed. Check the connection on your phone.";setStatus(message);await speak(message);}}
 finally{if(alive.current){state("ready");setBusy(false);}}};
 useEffect(()=>{alive.current=true;void turn();return()=>{alive.current=false;void recording.current?.stop().catch(()=>{});void Speech.stop();state("ready");};},[]);
 return <SafeAreaView style={{flex:1,backgroundColor:c.bg,padding:24}}><Pressable accessibilityRole="button" onPress={()=>router.canGoBack()?router.back():router.replace("/")}><Text style={{color:c.accent}}>‹ Back</Text></Pressable><View style={{flex:1,justifyContent:"center",gap:24}}><Text style={{color:c.textPrimary,fontSize:22}}>SSH voice</Text><Text style={{color:c.textSecondary}}>{status}</Text><Pressable accessibilityRole="button" disabled={busy} onPress={()=>void turn()} style={{padding:24,backgroundColor:c.bgCard,borderRadius:16}}><Text style={{color:c.textPrimary}}>Speak to selected pane</Text></Pressable><Pressable accessibilityRole="button" disabled={busy} onPress={()=>void saveSSHVoiceTarget(null).then(()=>router.replace("/car-voice-coding"))}><Text style={{color:c.textMuted}}>Use Yaver tasks for CarPlay instead</Text></Pressable></View></SafeAreaView>;
}
