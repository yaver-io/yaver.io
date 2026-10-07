import AsyncStorage from "@react-native-async-storage/async-storage";
import {loadSSHHosts,loadSSHCredentials,sshCall,sshInput,type SSHPane} from "./plainSSH";
const key="yaver.plainSSH.voiceTarget.v1";
export interface SSHVoiceTarget {hostId:string;pane:SSHPane}
export async function saveSSHVoiceTarget(value:SSHVoiceTarget|null){if(value)await AsyncStorage.setItem(key,JSON.stringify(value));else await AsyncStorage.removeItem(key);}
export async function loadSSHVoiceTarget():Promise<SSHVoiceTarget|null>{return JSON.parse(await AsyncStorage.getItem(key)||"null");}
/** One explicit spoken submission; no runner restart, cloud token or replay. */
export async function sendSSHVoice(text:string){
 const target=await loadSSHVoiceTarget();if(!target)throw new Error("Choose a pane in Plain SSH on your phone first.");
 const host=(await loadSSHHosts()).find(item=>item.id===target.hostId);if(!host)throw new Error("The saved SSH host was removed. Choose another on your phone.");
 const credentials=await loadSSHCredentials(host.id);const {id}=await sshCall<{id:string}>({...host,...credentials,op:"connect"});
 try{const panes=await sshCall<SSHPane[]>({op:"panes",id});const pane=panes.find(p=>p.id===target.pane.id&&p.sessionId===target.pane.sessionId&&p.identity===target.pane.identity);if(!pane)throw new Error("Your selected pane has ended. Choose a current pane on your phone.");
 await sshCall({op:"open",id,pane:pane.id,session:pane.sessionId,identity:pane.identity});await sshCall({op:"submit",id,data:sshInput(text)});
 }finally{await sshCall({op:"close",id}).catch(()=>{});}
}
