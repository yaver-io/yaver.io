import test from "node:test";
import assert from "node:assert/strict";
import { refreshSession } from "./auth.ts";
const handler = (refreshSession as unknown as { _handler: (ctx:any,args:any)=>Promise<any> })._handler;
function fixture(previousUntil:number, revoked=false) {
 const row={_id:"session",userId:"user",tokenHash:"current-hash",prevTokenHash:"previous-hash",prevTokenValidUntil:previousUntil,expiresAt:Date.now()+86400000};
 let patch:any;
 const ctx={db:{query:()=>({withIndex:(_index:string, select:any)=>{let field="",value="";select({eq:(f:string,v:string)=>{field=f;value=v;}});return {unique:async()=>!revoked && (row as any)[field]===value?row:null};}}),patch:async(_id:string,p:any)=>{patch=p;}}};
 return {ctx,patch:()=>patch};
}
test("previous token recovers a lost rotation response within grace",async()=>{
 const f=fixture(Date.now()+60000);
 const result=await handler(f.ctx,{tokenHash:"previous-hash",newTokenHash:"replacement-hash"});
 assert.equal(result.rotated,true);assert.equal(f.patch().tokenHash,"replacement-hash");assert.equal(f.patch().prevTokenHash,"current-hash");
});
test("expired previous token cannot renew",async()=>{
 const f=fixture(Date.now()-1);assert.equal(await handler(f.ctx,{tokenHash:"previous-hash",newTokenHash:"replacement-hash"}),null);assert.equal(f.patch(),undefined);
});
test("revoked sessions cannot renew",async()=>{
 const f=fixture(Date.now()+60000,true);assert.equal(await handler(f.ctx,{tokenHash:"previous-hash"}),null);assert.equal(f.patch(),undefined);
});
