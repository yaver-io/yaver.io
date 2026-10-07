"use strict";
const {test}=require("node:test");const assert=require("node:assert/strict");const {trustedSSHCaller,PlainSSH}=require("../src/plain-ssh");
test("SSH IPC is only available to the trusted top-level SSH screen",()=>{
 const origins=new Set(["https://yaver.io"]);const frame={url:"https://yaver.io/ssh"};const event={senderFrame:frame,sender:{mainFrame:frame}};
 assert.equal(trustedSSHCaller(event,origins),true);
 for(const url of ["https://evil.example/ssh","https://yaver.io/dashboard","https://yaver.io/ssh/other","https://yaver.io.evil.example/ssh"]){frame.url=url;assert.equal(trustedSSHCaller(event,origins),false);}
 frame.url="https://yaver.io/ssh";event.sender.mainFrame={url:frame.url};assert.equal(trustedSSHCaller(event,origins),false);
});
test("renderer cannot close another surface's SSH sessions",async()=>{await assert.rejects(new PlainSSH("unused").invoke({op:"closeAll"}),/Invalid SSH/);});
