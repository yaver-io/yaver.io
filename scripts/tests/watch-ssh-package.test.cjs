const test=require('node:test');const assert=require('node:assert/strict');const fs=require('node:fs');const vm=require('node:vm');const path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'../add-watch-ios-target.js'),'utf8');
for(const relativePath of ['../../apple/PlainSSH','"../../apple/PlainSSH"']){
 test(`Watch package setup reuses ${relativePath}`,()=>{
  const objects={PBXProject:{project:{packageReferences:[{value:'package'}]}},XCLocalSwiftPackageReference:{package:{relativePath}},XCSwiftPackageProductDependency:{product:{productName:'PlainSSH',package:'package'}},PBXNativeTarget:{watch:{packageProductDependencies:[{value:'product'}],buildPhases:[{value:'frameworks'}]}},PBXFrameworksBuildPhase:{frameworks:{files:[{value:'build'}]}},PBXBuildFile:{build:{productRef:'product'}}};
  const proj={hash:{project:{objects}},getFirstProject:()=>({uuid:'project'}),generateUuid:()=>{throw new Error('created duplicate reference')}};
  const ensure=vm.runInNewContext(`(${source.slice(source.indexOf('function ensurePlainSSH('))})`,{proj});
  ensure('watch');ensure('watch');assert.equal(objects.PBXProject.project.packageReferences.length,1);
 });
}
