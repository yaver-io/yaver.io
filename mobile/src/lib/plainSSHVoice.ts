import AsyncStorage from "@react-native-async-storage/async-storage";
import {loadSSHHosts,loadSSHCredentials,sshCall,sshInput,type SSHPane} from "./plainSSH";
import {appendTerminalSpeechText,terminalSpeechExcerpt} from "./terminalSpeech";
const key="yaver.plainSSH.voiceTarget.v1";
export interface SSHVoiceTarget {hostId:string;pane:SSHPane}
export async function saveSSHVoiceTarget(value:SSHVoiceTarget|null){if(value)await AsyncStorage.setItem(key,JSON.stringify(value));else await AsyncStorage.removeItem(key);}
export async function loadSSHVoiceTarget():Promise<SSHVoiceTarget|null>{return JSON.parse(await AsyncStorage.getItem(key)||"null");}
/** Submit only after spoken confirmation; undefined text means read-only.
 * A quiet screen is not proof that a runner finished. Return a bounded excerpt
 * explicitly labelled as current output, never an invented completion. */
export async function sendSSHVoice(text?:string,isCancelled=()=>false):Promise<string>{
 const target=await loadSSHVoiceTarget();if(!target)throw new Error("Choose a pane in Plain SSH on your phone first.");
 const host=(await loadSSHHosts()).find(item=>item.id===target.hostId);if(!host)throw new Error("The saved SSH host was removed. Choose another on your phone.");
 const credentials=await loadSSHCredentials(host.id);const {id}=await sshCall<{id:string}>({...host,...credentials,op:"connect"});
 let draining=true;
 try{
  if(isCancelled())return "";
  const panes=await sshCall<SSHPane[]>({op:"panes",id});const pane=panes.find(p=>p.id===target.pane.id&&p.sessionId===target.pane.sessionId&&p.identity===target.pane.identity);if(!pane)throw new Error("Your selected pane has ended. Choose a current pane on your phone.");
  await sshCall({op:"open",id,pane:pane.id,session:pane.sessionId,identity:pane.identity});
  void(async()=>{try{while(draining)await sshCall({op:"read",id});}catch{/* snapshot names connection failures */}})();
  const before=await sshCall<string>({op:"snapshot",id});
  if(text===undefined)return terminalSpeechExcerpt(appendTerminalSpeechText("",before));
  if(isCancelled())return "";
  await sshCall({op:"submit",id,data:sshInput(text)});
  const previous=new Set(before.split("\n").map(line=>line.trim()));let excerpt="",last="",quietSince=Date.now();const deadline=Date.now()+30000;
  while(!isCancelled()&&Date.now()<deadline){
   await new Promise(resolve=>setTimeout(resolve,1000));if(isCancelled())return "";
   const screen=await sshCall<string>({op:"snapshot",id});
   const fresh=screen.split("\n").filter(line=>!previous.has(line.trim())&&line.trim()!==text.trim()).join("\n");
   excerpt=terminalSpeechExcerpt(appendTerminalSpeechText("",fresh));
   if(screen!==last){last=screen;quietSince=Date.now();}else if(excerpt&&Date.now()-quietSince>=2000)break;
  }
  return excerpt;
 }finally{draining=false;await sshCall({op:"close",id}).catch(()=>{});}
}
