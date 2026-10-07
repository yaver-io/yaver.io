import React,{useEffect,useRef,useState} from "react";
import {NativeModules,Pressable,Text,View} from "react-native";
import {SafeAreaView} from "react-native-safe-area-context";
import {useRouter} from "expo-router";
import * as Speech from "expo-speech";
import {startRealtimeTranscribe} from "../src/lib/speech";
import {prepareNonInterruptingPlaybackAudioMode} from "../src/lib/microphoneAudioSession";
import {carVoiceEntryBus} from "../src/lib/carVoiceEntry";
import {loadSSHVoiceTarget,saveSSHVoiceTarget,sendSSHVoice} from "../src/lib/plainSSHVoice";
import {useColors} from "../src/context/ThemeContext";

/** Voice-only car surface. Every submission requires spoken confirmation.
 * Read pane, stop and cancel are local commands, never runner input. */
export default function PlainVoice(){
 const c=useColors();const router=useRouter();const [status,setStatus]=useState("Ready for your SSH pane");const [busy,setBusy]=useState(false);
 const alive=useRef(true);const running=useRef(false);const stopped=useRef(false);const recording=useRef<{stop:()=>Promise<string>}|null>(null);
 const state=(value:string)=>{NativeModules.YaverInfo?.setCarPlayVoiceState?.(value);};
 const cancelled=()=>!alive.current||stopped.current;
 const speak=(text:string)=>new Promise<void>(resolve=>{if(cancelled()){resolve();return;}state("speaking");Speech.speak(text,{onDone:resolve,onStopped:resolve,onError:()=>resolve()});});
 const listen=async(ms:number)=>{
  state("listening");const rec=await startRealtimeTranscribe(()=>{},{keepRecordingInBackground:true,sliceSec:1});if(cancelled()){await rec.stop();return "";}recording.current=rec;
  await new Promise(resolve=>setTimeout(resolve,ms));if(cancelled())return "";
  recording.current=null;const text=(await rec.stop()).trim();await prepareNonInterruptingPlaybackAudioMode();return text;
 };
 const normalize=(text:string)=>text.toLowerCase().replace(/[.!?]/g,"").trim();
 const stop=()=>{stopped.current=true;void recording.current?.stop().catch(()=>{});recording.current=null;void Speech.stop();void prepareNonInterruptingPlaybackAudioMode();state("ready");setStatus("Paused");};
 const start=async()=>{
  if(running.current)return;running.current=true;stopped.current=false;setBusy(true);
  try{
   if(!await loadSSHVoiceTarget())throw new Error("Choose a pane for CarPlay in Plain SSH on your phone.");
   while(!cancelled()){
    setStatus("Listening…");await speak("What would you like to send? You can also say read pane or stop.");if(cancelled())break;
    const text=await listen(7000);if(cancelled())break;
    if(!text){setStatus("Paused · no speech heard");await speak("Paused. Start SSH voice when you are ready.");break;}
    const command=normalize(text);if(["stop","pause","cancel","stop listening"].includes(command)){setStatus("Paused");await speak("Paused. Nothing was sent.");break;}
    const readOnly=["read pane","read response","read output","read latest output"].includes(command);
    if(!readOnly){
     setStatus("Waiting for spoken confirmation");await speak("Send this to your pane: "+text+". Say send or cancel.");if(cancelled())break;
     const confirmation=normalize(await listen(4000));if(cancelled())break;
     if(!["send","yes send","send it"].includes(confirmation)){await speak("Cancelled. Nothing was sent.");continue;}
    }
    state("working");setStatus(readOnly?"Reading your pane…":"Sending over SSH · waiting for pane output…");
    const output=await sendSSHVoice(readOnly?undefined:text,cancelled);if(cancelled())break;
    setStatus("Latest pane output");await speak(output?"Latest pane output. "+output:"No new readable output yet. Say read pane to check again.");
   }
  }catch(error){if(!cancelled()){const message=error instanceof Error?error.message:"SSH failed. Check the connection on your phone.";setStatus(message);await speak(message);}}
  finally{running.current=false;await prepareNonInterruptingPlaybackAudioMode().catch(()=>{});if(alive.current){state("ready");setBusy(false);}}
 };
 useEffect(()=>{alive.current=true;const unsubscribe=carVoiceEntryBus.subscribe(()=>void start());void start();return()=>{alive.current=false;stopped.current=true;unsubscribe();void recording.current?.stop().catch(()=>{});void Speech.stop();void prepareNonInterruptingPlaybackAudioMode();state("ready");};},[]);
 return <SafeAreaView style={{flex:1,backgroundColor:c.bg,padding:24}}>
  <Pressable accessibilityRole="button" onPress={()=>{stop();router.canGoBack()?router.back():router.replace("/");}}><Text style={{color:c.accent}}>‹ Back</Text></Pressable>
  <View style={{flex:1,justifyContent:"center",gap:24}}><Text style={{color:c.textPrimary,fontSize:22}}>SSH voice</Text><Text style={{color:c.textSecondary}}>{status}</Text>
   <Pressable accessibilityRole="button" onPress={()=>busy?stop():void start()} style={{padding:24,backgroundColor:c.bgCard,borderRadius:16}}><Text style={{color:c.textPrimary}}>{busy?"Pause SSH voice":"Speak to selected pane"}</Text></Pressable>
   <Pressable accessibilityRole="button" disabled={busy} onPress={()=>void saveSSHVoiceTarget(null).then(()=>router.replace("/car-voice-coding"))}><Text style={{color:c.textMuted}}>Use Yaver tasks for CarPlay instead</Text></Pressable>
  </View>
 </SafeAreaView>;
}
