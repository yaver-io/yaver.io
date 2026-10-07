"use strict";
const {spawn}=require("node:child_process");
const readline=require("node:readline");
const {randomUUID}=require("node:crypto");
const OPS=new Set(["probe","connect","panes","open","read","write","submit","close","enroll","installYaver","enrollmentRead"]);
class PlainSSH {
 constructor(binary){this.binary=binary;this.pending=new Map();this.child=null;}
 start(){if(this.child)return;const child=spawn(this.binary,[],{stdio:["pipe","pipe","ignore"]});this.child=child;
  const fail=()=>{if(this.child===child)this.child=null;for(const {reject,timer} of this.pending.values()){clearTimeout(timer);reject(new Error("SSH companion stopped. Reopen the SSH connection."));}this.pending.clear();};
  child.stdin.on("error",fail);child.on("error",fail);child.on("exit",fail);
  readline.createInterface({input:child.stdout}).on("line",line=>{try{const result=JSON.parse(line);const waiter=this.pending.get(result.id);if(!waiter)return;this.pending.delete(result.id);clearTimeout(waiter.timer);waiter.resolve(result.response);}catch{}});
 }
 invoke(request){if(!request||!OPS.has(request.op)||Buffer.byteLength(JSON.stringify(request))>256*1024)return Promise.reject(new Error("Invalid SSH operation."));this.start();
  return new Promise((resolve,reject)=>{const id=randomUUID();const timer=setTimeout(()=>{this.pending.delete(id);reject(new Error("SSH operation timed out. Check the pane before retrying input."));},45000);this.pending.set(id,{resolve,reject,timer});this.child.stdin.write(JSON.stringify({id,request})+"\n",error=>{if(error){clearTimeout(timer);this.pending.delete(id);reject(new Error("SSH companion is unavailable."));}});});
 }
 close(){this.child?.kill();this.child=null;}
}
function trustedSSHCaller(event,origins){try{const url=new URL(event.senderFrame.url);return event.senderFrame===event.sender.mainFrame && origins.has(url.origin) && url.pathname==="/ssh";}catch{return false;}}
module.exports={PlainSSH,OPS,trustedSSHCaller};
