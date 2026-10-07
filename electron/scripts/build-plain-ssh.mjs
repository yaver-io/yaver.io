import {execFileSync} from "node:child_process";
import {mkdirSync,unlinkSync} from "node:fs";
import {fileURLToPath} from "node:url";
import path from "node:path";
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),"../..");
const out=path.join(root,"electron/resources/plain-ssh");mkdirSync(out,{recursive:true});
const target=process.argv[2]||process.platform;
const goos={darwin:"darwin",win32:"windows",linux:"linux"}[target];if(!goos)throw new Error("Unsupported SSH companion platform");
const universal=target==="darwin";
for(const arch of universal?["arm64","amd64"]:[target==="win32"?"amd64":process.arch==="arm64"?"arm64":"amd64"]){
 execFileSync("go",["build","-trimpath","-ldflags=-s -w","-o",path.join(out,universal?`plainssh-${arch}`:`plainssh${target==="win32"?".exe":""}`),"./cmd/stdio"],{cwd:path.join(root,"mobile/native/plain-ssh/core"),env:{...process.env,GOOS:goos,GOARCH:arch,CGO_ENABLED:"0"},stdio:"inherit"});
}
if(universal){execFileSync("lipo",["-create",path.join(out,"plainssh-arm64"),path.join(out,"plainssh-amd64"),"-output",path.join(out,"plainssh")]);for(const arch of ["arm64","amd64"])unlinkSync(path.join(out,`plainssh-${arch}`));}
