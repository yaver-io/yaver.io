const test=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const path=require('node:path');
const {provisionStudio}=require('../src/postinstall-policy');
test('default and generic CI installs provision only connectivity',()=>{
 for(const env of [{},{CI:'1'},{YAVER_POSTINSTALL_STUDIO:'false'},{YAVER_POSTINSTALL_STUDIO:'1',YAVER_EDGE_LITE:'1'}])assert.equal(provisionStudio(env),false);
});
test('Studio and dedicated lab installs are explicit',()=>{
 for(const env of [{YAVER_POSTINSTALL_STUDIO:'1'},{YAVER_CI:'1'},{YAVER_COMPLETE_AUTOMATION_HOST:'true'}])assert.equal(provisionStudio(env),true);
});
test('connectivity gate returns before Hermes and runner provisioning',()=>{
 const source=fs.readFileSync(path.join(__dirname,'../src/postinstall.js'),'utf8');
 const start=source.indexOf('if (!provisionStudio())');assert.ok(start>0);
 const hermes=source.indexOf('await ensureHermesc',start);assert.ok(hermes>start);
 assert.match(source.slice(start,hermes),/return;/);
});
